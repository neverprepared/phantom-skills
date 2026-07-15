package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// UsageEvent is one telemetry observation for a skill.
type UsageEvent struct {
	SkillName string
	SessionID string
	Machine   string
	Event     string // invoked|helpful|ignored|error
	Context   map[string]any
	TS        time.Time
}

// IngestUsage inserts a batch of usage events, deduping on
// (profile, skill_name, session_id, event, ts) via ON CONFLICT DO NOTHING.
// Returns the number of rows actually accepted (new), so the caller can report
// how many were deduped.
func (s *Store) IngestUsage(ctx context.Context, profile string, events []UsageEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("pgstore: begin usage: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	accepted := 0
	for _, e := range events {
		if e.Event == "" || e.SkillName == "" {
			continue
		}
		ts := e.TS
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		var ctxJSON []byte
		if e.Context != nil {
			ctxJSON, _ = json.Marshal(e.Context)
		}
		tag, err := tx.Exec(ctx, `
INSERT INTO usage_events (profile, skill_name, session_id, machine, event, context, ts)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (profile, skill_name, session_id, event, ts) DO NOTHING`,
			profile, e.SkillName, e.SessionID, e.Machine, e.Event, ctxJSON, ts)
		if err != nil {
			return 0, fmt.Errorf("pgstore: insert usage: %w", err)
		}
		accepted += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("pgstore: commit usage: %w", err)
	}
	return accepted, nil
}
