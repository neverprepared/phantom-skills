package pgstore

import (
	"context"
	"fmt"
	"time"
)

// RatchetRun is one fired ratchet tracked in the auto-fire ledger.
type RatchetRun struct {
	ID        int64     `json:"id"`
	SkillName string    `json:"skill_name"`
	Kind      string    `json:"kind"`
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Branch    string    `json:"branch"`
	Status    string    `json:"status"`
	PRURL     string    `json:"pr_url"`
	Rationale string    `json:"rationale"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// inFlightStatuses are the states that count as an active ratchet for dedup and
// rate-limiting: still working, or its PR is open awaiting a decision.
const inFlightStatuses = `('fired','pr_open')`

// RecordRatchet inserts a ledger row for a freshly-fired ratchet.
func (s *Store) RecordRatchet(ctx context.Context, profile string, r RatchetRun) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO ratchet_runs (profile, skill_name, kind, task_id, job_id, branch, status, rationale)
VALUES ($1,$2,$3,$4,$5,$6,COALESCE(NULLIF($7,''),'fired'),$8)
RETURNING id`,
		profile, r.SkillName, r.Kind, r.TaskID, r.JobID, r.Branch, r.Status, r.Rationale).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("pgstore: record ratchet: %w", err)
	}
	return id, nil
}

// HasInFlightRatchet reports whether the skill already has a ratchet working or
// a PR open — the dedup guard (never fire a duplicate).
func (s *Store) HasInFlightRatchet(ctx context.Context, profile, skill string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ratchet_runs WHERE profile=$1 AND skill_name=$2 AND status IN `+inFlightStatuses,
		profile, skill).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("pgstore: in-flight check: %w", err)
	}
	return n > 0, nil
}

// RecentlyRatcheted reports whether the skill had any ratchet within the
// cooldown window — the anti-nag guard (don't re-propose right after a run,
// especially one a human closed).
func (s *Store) RecentlyRatcheted(ctx context.Context, profile, skill string, within time.Duration) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ratchet_runs WHERE profile=$1 AND skill_name=$2 AND created_at > $3`,
		profile, skill, time.Now().Add(-within)).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("pgstore: recent check: %w", err)
	}
	return n > 0, nil
}

// CountInFlightRatchets returns the number of active ratchets for the profile —
// the rate-limit input (cap concurrent ratchets).
func (s *Store) CountInFlightRatchets(ctx context.Context, profile string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ratchet_runs WHERE profile=$1 AND status IN `+inFlightStatuses, profile).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("pgstore: in-flight count: %w", err)
	}
	return n, nil
}

// UpdateRatchetStatus advances a run's status (and PR url when it opens).
func (s *Store) UpdateRatchetStatus(ctx context.Context, id int64, status, prURL string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE ratchet_runs SET status=$2, pr_url=COALESCE(NULLIF($3,''), pr_url) WHERE id=$1`,
		id, status, prURL)
	if err != nil {
		return fmt.Errorf("pgstore: update ratchet status: %w", err)
	}
	return nil
}

// ListRatchets returns ledger rows for a profile filtered by optional status,
// newest first.
func (s *Store) ListRatchets(ctx context.Context, profile, status string) ([]RatchetRun, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, skill_name, kind, task_id, job_id, branch, status, pr_url, rationale, created_at, updated_at
FROM ratchet_runs
WHERE profile=$1 AND ($2='' OR status=$2)
ORDER BY id DESC LIMIT 500`, profile, status)
	if err != nil {
		return nil, fmt.Errorf("pgstore: list ratchets: %w", err)
	}
	defer rows.Close()
	var out []RatchetRun
	for rows.Next() {
		var r RatchetRun
		if err := rows.Scan(&r.ID, &r.SkillName, &r.Kind, &r.TaskID, &r.JobID, &r.Branch,
			&r.Status, &r.PRURL, &r.Rationale, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("pgstore: scan ratchet: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
