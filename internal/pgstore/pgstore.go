// Package pgstore is the Postgres System of Record for the shared skills
// registry: skill identity (skills) and immutable content-addressed versions
// (skill_versions). Unlike phantom-brain's per-profile database split, the
// skills registry is a single small database (phantom_skills); tenancy is a
// `profile` column, not a separate database.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// pgx5 database driver, registered for its side effect (the "pgx5://"
	// scheme golang-migrate dispatches on).
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/neverprepared/phantom-skills/internal/skillfile"
	"github.com/neverprepared/phantom-skills/migrations"
)

// ErrNotFound is returned by lookups when no matching skill exists.
var ErrNotFound = errors.New("pgstore: not found")

// Store wraps a pgx connection pool over the phantom_skills database.
type Store struct {
	pool *pgxpool.Pool
}

// Open dials the pool and verifies connectivity. Caller owns Close.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgstore: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgstore: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// Ping reports database reachability (used by /health).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Skill is one logical skill's identity + status.
type Skill struct {
	ID        int64     `json:"-"`
	Profile   string    `json:"-"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	Origin    string    `json:"origin"`
	Tags      []string  `json:"tags"`
	Version   int       `json:"version"` // current version number (0 if none)
	SHA       string    `json:"sha"`     // current version sha ("" if none)
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillVersion is one immutable, content-addressed version. Frontmatter and
// Body are omitted when empty so version-history listings (which don't fetch
// content) stay lean while a full skill GET still carries them.
type SkillVersion struct {
	Version     int            `json:"version"`
	SHA         string         `json:"sha"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Body        string         `json:"body,omitempty"`
	Author      string         `json:"author"`
	Source      string         `json:"source"`
	CreatedAt   time.Time      `json:"created_at"`
}

// ListOpts filters and pages ListSkills. Keyset pagination on id (AfterID).
type ListOpts struct {
	Status  string
	Tag     string
	AfterID int64
	Limit   int
}

// ListSkills returns a keyset page of skills for a profile, ascending by id.
func (s *Store) ListSkills(ctx context.Context, profile string, opts ListOpts) ([]Skill, error) {
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = 100
	}
	const q = `
SELECT s.id, s.name, s.slug, s.status, s.origin, s.tags,
       COALESCE(v.version, 0), COALESCE(v.sha, ''),
       s.created_at, s.updated_at
FROM skills s
LEFT JOIN skill_versions v ON v.id = s.current_version_id
WHERE s.profile = $1
  AND s.id > $2
  AND ($3 = '' OR s.status = $3)
  AND ($4 = '' OR $4 = ANY(s.tags))
ORDER BY s.id ASC
LIMIT $5`
	rows, err := s.pool.Query(ctx, q, profile, opts.AfterID, opts.Status, opts.Tag, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("pgstore: list skills: %w", err)
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		var sk Skill
		sk.Profile = profile
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Slug, &sk.Status, &sk.Origin, &sk.Tags,
			&sk.Version, &sk.SHA, &sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, fmt.Errorf("pgstore: scan skill: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// GetSkill returns a skill and its current version body/frontmatter.
func (s *Store) GetSkill(ctx context.Context, profile, name string) (*Skill, *SkillVersion, error) {
	const q = `
SELECT s.id, s.name, s.slug, s.status, s.origin, s.tags,
       s.created_at, s.updated_at,
       v.version, v.sha, v.frontmatter, v.body, v.author, v.source, v.created_at
FROM skills s
LEFT JOIN skill_versions v ON v.id = s.current_version_id
WHERE s.profile = $1 AND s.name = $2`
	row := s.pool.QueryRow(ctx, q, profile, name)
	var sk Skill
	sk.Profile = profile
	var (
		ver      *int
		sha      *string
		fmRaw    []byte
		body     *string
		author   *string
		source   *string
		vCreated *time.Time
	)
	if err := row.Scan(&sk.ID, &sk.Name, &sk.Slug, &sk.Status, &sk.Origin, &sk.Tags,
		&sk.CreatedAt, &sk.UpdatedAt, &ver, &sha, &fmRaw, &body, &author, &source, &vCreated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("pgstore: get skill: %w", err)
	}
	if ver == nil { // skill exists but has no current version yet
		return &sk, nil, nil
	}
	sk.Version = *ver
	sk.SHA = deref(sha)
	v := &SkillVersion{
		Version:   *ver,
		SHA:       deref(sha),
		Body:      deref(body),
		Author:    deref(author),
		Source:    deref(source),
		CreatedAt: derefTime(vCreated),
	}
	if len(fmRaw) > 0 {
		_ = json.Unmarshal(fmRaw, &v.Frontmatter)
	}
	return &sk, v, nil
}

// RegisterInput is the payload for registering a new skill version.
type RegisterInput struct {
	Profile     string
	Name        string
	Frontmatter map[string]any
	Body        string
	Author      string
	Source      string
	Origin      string // only used when the skill row is created
	Status      string // only used when the skill row is created (default "draft")
	Tags        []string
}

// RegisterVersion upserts the skill identity and appends a new content-addressed
// version, updating current_version_id. Re-registering identical content
// (same canonical sha for this skill) is a no-op that returns the existing
// version with created=false.
func (s *Store) RegisterVersion(ctx context.Context, in RegisterInput) (*SkillVersion, bool, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Profile) == "" {
		return nil, false, fmt.Errorf("pgstore: profile and name are required")
	}
	sha := skillfile.CanonicalSHA(in.Frontmatter, in.Body)
	fmJSON, err := json.Marshal(orEmptyMap(in.Frontmatter))
	if err != nil {
		return nil, false, fmt.Errorf("pgstore: marshal frontmatter: %w", err)
	}
	status := in.Status
	if status == "" {
		status = "draft"
	}
	origin := in.Origin
	if origin == "" {
		origin = "authored"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("pgstore: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	var skillID int64
	err = tx.QueryRow(ctx, `
INSERT INTO skills (profile, name, slug, status, origin, tags)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (profile, name) DO UPDATE SET updated_at = now()
RETURNING id`,
		in.Profile, in.Name, skillfile.Slugify(in.Name), status, origin, orEmptySlice(in.Tags)).Scan(&skillID)
	if err != nil {
		return nil, false, fmt.Errorf("pgstore: upsert skill: %w", err)
	}

	// Idempotency: identical content for this skill returns the existing version.
	var existing SkillVersion
	err = tx.QueryRow(ctx,
		`SELECT version, sha, body, author, source, created_at
		 FROM skill_versions WHERE skill_id = $1 AND sha = $2`, skillID, sha).
		Scan(&existing.Version, &existing.SHA, &existing.Body, &existing.Author, &existing.Source, &existing.CreatedAt)
	if err == nil {
		existing.Frontmatter = orEmptyMap(in.Frontmatter)
		if cErr := tx.Commit(ctx); cErr != nil {
			return nil, false, fmt.Errorf("pgstore: commit: %w", cErr)
		}
		return &existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("pgstore: check existing version: %w", err)
	}

	var nextVer int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM skill_versions WHERE skill_id = $1`, skillID).
		Scan(&nextVer); err != nil {
		return nil, false, fmt.Errorf("pgstore: next version: %w", err)
	}

	var newVersionID int64
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
INSERT INTO skill_versions (skill_id, version, sha, frontmatter, body, author, source)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, created_at`,
		skillID, nextVer, sha, fmJSON, in.Body, in.Author, in.Source).Scan(&newVersionID, &createdAt); err != nil {
		return nil, false, fmt.Errorf("pgstore: insert version: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE skills SET current_version_id = $1 WHERE id = $2`, newVersionID, skillID); err != nil {
		return nil, false, fmt.Errorf("pgstore: set current version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("pgstore: commit: %w", err)
	}
	return &SkillVersion{
		Version:     nextVer,
		SHA:         sha,
		Frontmatter: orEmptyMap(in.Frontmatter),
		Body:        in.Body,
		Author:      in.Author,
		Source:      in.Source,
		CreatedAt:   createdAt,
	}, true, nil
}

// ListVersions returns every version of a skill, newest first.
func (s *Store) ListVersions(ctx context.Context, profile, name string) ([]SkillVersion, error) {
	const q = `
SELECT v.version, v.sha, v.author, v.source, v.created_at
FROM skill_versions v
JOIN skills s ON s.id = v.skill_id
WHERE s.profile = $1 AND s.name = $2
ORDER BY v.version DESC`
	rows, err := s.pool.Query(ctx, q, profile, name)
	if err != nil {
		return nil, fmt.Errorf("pgstore: list versions: %w", err)
	}
	defer rows.Close()
	var out []SkillVersion
	for rows.Next() {
		var v SkillVersion
		if err := rows.Scan(&v.Version, &v.SHA, &v.Author, &v.Source, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("pgstore: scan version: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		// Distinguish "no such skill" from "skill with zero versions": a skill
		// always has ≥1 version once registered, so empty ⇒ not found.
		if _, _, err := s.GetSkill(ctx, profile, name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SyncRow is one entry in the sync change-feed.
type SyncRow struct {
	ID          int64
	Name        string
	Slug        string
	Status      string
	Origin      string
	SHA         string
	Frontmatter map[string]any
	Body        string
	UpdatedAt   time.Time
}

// SyncFeed returns skills whose (updated_at, id) sorts after the given cursor,
// ascending, for the agent change-feed. A composite keyset on (updated_at, id)
// is used rather than id alone so that a NEW VERSION of an existing skill (which
// bumps updated_at via the trigger but keeps the same id) reappears in the feed.
// Promoted rows carry their current version's content; the handler partitions
// promoted→materialize, retired→delete, and skips other statuses (they still
// advance the cursor).
func (s *Store) SyncFeed(ctx context.Context, profile string, sinceTS time.Time, sinceID int64, limit int) ([]SyncRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
SELECT s.id, s.name, s.slug, s.status, s.origin, s.updated_at,
       COALESCE(v.sha, ''), v.frontmatter, COALESCE(v.body, '')
FROM skills s
LEFT JOIN skill_versions v ON v.id = s.current_version_id
WHERE s.profile = $1
  AND (s.updated_at, s.id) > ($2, $3)
ORDER BY s.updated_at ASC, s.id ASC
LIMIT $4`
	rows, err := s.pool.Query(ctx, q, profile, sinceTS, sinceID, limit)
	if err != nil {
		return nil, fmt.Errorf("pgstore: sync feed: %w", err)
	}
	defer rows.Close()
	var out []SyncRow
	for rows.Next() {
		var r SyncRow
		var fmRaw []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Slug, &r.Status, &r.Origin, &r.UpdatedAt,
			&r.SHA, &fmRaw, &r.Body); err != nil {
			return nil, fmt.Errorf("pgstore: scan sync row: %w", err)
		}
		if len(fmRaw) > 0 {
			_ = json.Unmarshal(fmRaw, &r.Frontmatter)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RetireSkill soft-deletes a skill (status=retired). Returns ErrNotFound if the
// skill doesn't exist.
func (s *Store) RetireSkill(ctx context.Context, profile, name string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE skills SET status = 'retired' WHERE profile = $1 AND name = $2`, profile, name)
	if err != nil {
		return fmt.Errorf("pgstore: retire: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Migrate applies all pending UP migrations against dsn using the embedded
// source. Accepts postgres://, postgresql://, or pgx5:// schemes.
func Migrate(dsn string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("pgstore: open migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toPgxURL(dsn))
	if err != nil {
		return fmt.Errorf("pgstore: init migrator: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("pgstore: migrate up: %w", err)
	}
	return nil
}

// MigrationStatus returns the current migration version and whether the schema
// is in a dirty (failed-migration) state.
func MigrationStatus(dsn string) (version uint, dirty bool, err error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return 0, false, fmt.Errorf("pgstore: open migration source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toPgxURL(dsn))
	if err != nil {
		return 0, false, fmt.Errorf("pgstore: init migrator: %w", err)
	}
	defer m.Close()
	version, dirty, err = m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}

// toPgxURL rewrites a Postgres DSN scheme to "pgx5://" for the golang-migrate
// pgx5 driver.
func toPgxURL(dsn string) string {
	switch {
	case strings.HasPrefix(dsn, "pgx5://"):
		return dsn
	case strings.HasPrefix(dsn, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(dsn, "postgresql://")
	case strings.HasPrefix(dsn, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(dsn, "postgres://")
	default:
		return dsn
	}
}
