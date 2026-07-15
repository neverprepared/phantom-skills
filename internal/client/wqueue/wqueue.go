// Package wqueue is the agent-side SQLite write-ahead queue. Writes the agent
// makes to the daemon (usage telemetry, skill proposals) are enqueued here
// first, then attempted immediately; if the daemon is unreachable the row
// survives and a background drainer replays it later. This is what lets an
// offline Claude Code session keep recording usage without losing data.
//
// Deliberately simpler than phantom-brain's wqueue: no attachment staging (the
// skills payloads are small JSON), no per-kind SHA dedup. Kept correct on the
// essentials — atomic enqueue, eligible-only drain, exponential backoff, and
// dead-lettering after a retry ceiling.
package wqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// maxAttempts is the retry ceiling before an item is dead-lettered. At the
// backoff schedule below this is ~roughly a day of retries.
const maxAttempts = 12

// Kind tags what daemon endpoint an item replays to.
type Kind string

const (
	KindUsage    Kind = "usage"
	KindProposal Kind = "proposal"
)

func (k Kind) valid() bool { return k == KindUsage || k == KindProposal }

// Item is one queued write.
type Item struct {
	ID            int64
	Kind          Kind
	Payload       []byte
	Attempts      int
	CreatedAt     time.Time
	NextAttemptAt time.Time
	LastError     string
	Dead          bool
}

// Queue is a handle to the on-disk write-ahead queue.
type Queue struct {
	db  *sql.DB
	dir string
}

// Open opens (creating if needed) the queue at {dir}/wqueue.sqlite.
func Open(dir string) (*Queue, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("wqueue: mkdir %s: %w", dir, err)
	}
	dsn := "file:" + filepath.Join(dir, "wqueue.sqlite") + "?_busy_timeout=5000&_journal_mode=WAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("wqueue: open: %w", err)
	}
	db.SetMaxOpenConns(1) // single writer; avoids SQLITE_BUSY under WAL
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS wqueue (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    kind            TEXT    NOT NULL,
    payload         BLOB    NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT '',
    dead            INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("wqueue: create schema: %w", err)
	}
	return &Queue{db: db, dir: dir}, nil
}

// Close closes the underlying database.
func (q *Queue) Close() error { return q.db.Close() }

// Dir returns the queue's directory.
func (q *Queue) Dir() string { return q.dir }

// Enqueue appends a write. It becomes immediately eligible (next_attempt_at=0).
func (q *Queue) Enqueue(ctx context.Context, kind Kind, payload []byte) (*Item, error) {
	if !kind.valid() {
		return nil, fmt.Errorf("wqueue: invalid kind %q", kind)
	}
	now := time.Now()
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO wqueue (kind, payload, created_at, next_attempt_at) VALUES (?, ?, ?, 0)`,
		string(kind), payload, now.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("wqueue: enqueue: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Item{ID: id, Kind: kind, Payload: payload, CreatedAt: now}, nil
}

// Depth returns the number of live (non-dead) items.
func (q *Queue) Depth(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wqueue WHERE dead = 0`).Scan(&n)
	return n, err
}

// NextEligible returns up to limit live items whose next_attempt_at <= now,
// oldest first.
func (q *Queue) NextEligible(ctx context.Context, now time.Time, limit int) ([]*Item, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := q.db.QueryContext(ctx, `
SELECT id, kind, payload, attempts, created_at, next_attempt_at, last_error, dead
FROM wqueue
WHERE dead = 0 AND next_attempt_at <= ?
ORDER BY id ASC
LIMIT ?`, now.UnixNano(), limit)
	if err != nil {
		return nil, fmt.Errorf("wqueue: next eligible: %w", err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// MarkAttempt records a failed attempt: bumps attempts, schedules the next try
// with exponential backoff, and dead-letters once the ceiling is reached.
func (q *Queue) MarkAttempt(ctx context.Context, id int64, now time.Time, attemptErr error) error {
	var attempts int
	if err := q.db.QueryRowContext(ctx, `SELECT attempts FROM wqueue WHERE id = ?`, id).Scan(&attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // already deleted; nothing to do
		}
		return fmt.Errorf("wqueue: read attempts: %w", err)
	}
	attempts++
	msg := ""
	if attemptErr != nil {
		msg = attemptErr.Error()
	}
	if attempts >= maxAttempts {
		_, err := q.db.ExecContext(ctx,
			`UPDATE wqueue SET attempts = ?, last_error = ?, dead = 1 WHERE id = ?`, attempts, msg, id)
		return err
	}
	next := now.Add(BackoffFor(attempts))
	_, err := q.db.ExecContext(ctx,
		`UPDATE wqueue SET attempts = ?, next_attempt_at = ?, last_error = ? WHERE id = ?`,
		attempts, next.UnixNano(), msg, id)
	return err
}

// Delete removes an item (called after a successful replay).
func (q *Queue) Delete(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, `DELETE FROM wqueue WHERE id = ?`, id)
	return err
}

// List returns items ordered by id. includeDead controls whether dead-lettered
// rows are included.
func (q *Queue) List(ctx context.Context, includeDead bool, limit int) ([]*Item, error) {
	if limit <= 0 {
		limit = 100
	}
	where := "WHERE dead = 0"
	if includeDead {
		where = ""
	}
	rows, err := q.db.QueryContext(ctx, `
SELECT id, kind, payload, attempts, created_at, next_attempt_at, last_error, dead
FROM wqueue `+where+`
ORDER BY id ASC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("wqueue: list: %w", err)
	}
	defer rows.Close()
	return scanItems(rows)
}

// Purge deletes rows. deadOnly restricts to dead-lettered items; all=true wipes
// everything. Returns the number removed.
func (q *Queue) Purge(ctx context.Context, deadOnly, all bool) (int, error) {
	q0 := `DELETE FROM wqueue WHERE dead = 1`
	switch {
	case all:
		q0 = `DELETE FROM wqueue`
	case deadOnly:
		q0 = `DELETE FROM wqueue WHERE dead = 1`
	default:
		q0 = `DELETE FROM wqueue WHERE dead = 1`
	}
	res, err := q.db.ExecContext(ctx, q0)
	if err != nil {
		return 0, fmt.Errorf("wqueue: purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// BackoffFor returns the delay before retry number `attempts`. Exponential
// (base 2s) capped at 30 minutes.
func BackoffFor(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	const base = 2 * time.Second
	const cap = 30 * time.Minute
	d := time.Duration(math.Pow(2, float64(attempts-1))) * base
	if d > cap || d <= 0 {
		return cap
	}
	return d
}

func scanItems(rows *sql.Rows) ([]*Item, error) {
	var out []*Item
	for rows.Next() {
		var (
			it      Item
			kind    string
			created int64
			next    int64
			deadInt int
		)
		if err := rows.Scan(&it.ID, &kind, &it.Payload, &it.Attempts, &created, &next, &it.LastError, &deadInt); err != nil {
			return nil, fmt.Errorf("wqueue: scan: %w", err)
		}
		it.Kind = Kind(kind)
		it.CreatedAt = time.Unix(0, created)
		if next > 0 {
			it.NextAttemptAt = time.Unix(0, next)
		}
		it.Dead = deadInt != 0
		out = append(out, &it)
	}
	return out, rows.Err()
}
