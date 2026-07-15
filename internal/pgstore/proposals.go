package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Proposal is a create/prune/promote decision awaiting (or past) a human gate.
type Proposal struct {
	ID                  int64          `json:"id"`
	Kind                string         `json:"kind"`
	SkillName           string         `json:"skill_name"`
	ProposedFrontmatter map[string]any `json:"proposed_frontmatter,omitempty"`
	ProposedBody        string         `json:"proposed_body,omitempty"`
	Diff                string         `json:"diff,omitempty"`
	Rationale           string         `json:"rationale"`
	Evidence            map[string]any `json:"evidence,omitempty"`
	Score               *float64       `json:"score,omitempty"`
	VerifierReport      map[string]any `json:"verifier_report,omitempty"`
	Status              string         `json:"status"`
	CreatedBy           string         `json:"created_by"`
	DecidedBy           string         `json:"decided_by,omitempty"`
	DecidedReason       string         `json:"decided_reason,omitempty"`
	DecidedAt           *time.Time     `json:"decided_at,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
}

// ProposalInput creates a proposal.
type ProposalInput struct {
	Profile             string
	Kind                string // create|prune|promote
	SkillName           string
	ProposedFrontmatter map[string]any
	ProposedBody        string
	Diff                string
	Rationale           string
	Evidence            map[string]any
	Score               *float64
	VerifierReport      map[string]any
	CreatedBy           string
}

// ApplyResult reports what an approval did.
type ApplyResult struct {
	Kind      string `json:"kind"`
	SkillName string `json:"skill_name"`
	Version   int    `json:"version,omitempty"`
	NewStatus string `json:"new_status,omitempty"`
}

var validProposalKind = map[string]bool{"create": true, "prune": true, "promote": true}

// CreateProposal inserts a pending proposal and returns its id.
func (s *Store) CreateProposal(ctx context.Context, in ProposalInput) (int64, error) {
	if !validProposalKind[in.Kind] {
		return 0, fmt.Errorf("pgstore: invalid proposal kind %q", in.Kind)
	}
	if in.SkillName == "" {
		return 0, fmt.Errorf("pgstore: proposal skill_name required")
	}
	fmJSON := jsonOrNil(in.ProposedFrontmatter)
	evJSON := jsonOrNil(in.Evidence)
	vrJSON := jsonOrNil(in.VerifierReport)
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO proposals
  (profile, kind, skill_name, proposed_frontmatter, proposed_body, diff, rationale, evidence, score, verifier_report, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING id`,
		in.Profile, in.Kind, in.SkillName, fmJSON, in.ProposedBody, in.Diff, in.Rationale,
		evJSON, in.Score, vrJSON, in.CreatedBy).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("pgstore: create proposal: %w", err)
	}
	return id, nil
}

// ListProposals returns proposal summaries (no body/diff) filtered by optional
// status and kind, newest first.
func (s *Store) ListProposals(ctx context.Context, profile, status, kind string) ([]Proposal, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, kind, skill_name, rationale, score, status, created_by, decided_by, decided_reason, decided_at, created_at
FROM proposals
WHERE profile = $1
  AND ($2 = '' OR status = $2)
  AND ($3 = '' OR kind = $3)
ORDER BY id DESC
LIMIT 500`, profile, status, kind)
	if err != nil {
		return nil, fmt.Errorf("pgstore: list proposals: %w", err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		var p Proposal
		var decidedBy, decidedReason *string
		if err := rows.Scan(&p.ID, &p.Kind, &p.SkillName, &p.Rationale, &p.Score, &p.Status,
			&p.CreatedBy, &decidedBy, &decidedReason, &p.DecidedAt, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("pgstore: scan proposal: %w", err)
		}
		p.DecidedBy = deref(decidedBy)
		p.DecidedReason = deref(decidedReason)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProposal returns one proposal with full detail (body, diff, evidence).
func (s *Store) GetProposal(ctx context.Context, profile string, id int64) (*Proposal, error) {
	row := s.pool.QueryRow(ctx, `
SELECT id, kind, skill_name, proposed_frontmatter, proposed_body, diff, rationale,
       evidence, score, verifier_report, status, created_by, decided_by, decided_reason, decided_at, created_at
FROM proposals WHERE profile = $1 AND id = $2`, profile, id)
	return scanProposalFull(row)
}

// RejectProposal marks a pending proposal rejected.
func (s *Store) RejectProposal(ctx context.Context, profile string, id int64, decidedBy, reason string) error {
	tag, err := s.pool.Exec(ctx, `
UPDATE proposals SET status='rejected', decided_by=$3, decided_reason=$4, decided_at=now()
WHERE profile=$1 AND id=$2 AND status='pending'`, profile, id, decidedBy, reason)
	if err != nil {
		return fmt.Errorf("pgstore: reject: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ApproveProposal applies a pending proposal to the registry and marks it
// approved, atomically. create → new skill version (status 'local'); promote →
// status 'promoted' + promotion_state; prune → status 'retired'.
func (s *Store) ApproveProposal(ctx context.Context, profile string, id int64, decidedBy string) (*ApplyResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgstore: begin approve: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the row so a concurrent approve/reject can't double-apply.
	row := tx.QueryRow(ctx, `
SELECT id, kind, skill_name, proposed_frontmatter, proposed_body, diff, rationale,
       evidence, score, verifier_report, status, created_by, decided_by, decided_reason, decided_at, created_at
FROM proposals WHERE profile=$1 AND id=$2 AND status='pending' FOR UPDATE`, profile, id)
	p, err := scanProposalFull(row)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	res := &ApplyResult{Kind: p.Kind, SkillName: p.SkillName}
	switch p.Kind {
	case "create":
		ver, _, err := registerVersionTx(ctx, tx, RegisterInput{
			Profile:     profile,
			Name:        p.SkillName,
			Frontmatter: p.ProposedFrontmatter,
			Body:        p.ProposedBody,
			Author:      p.CreatedBy,
			Source:      "proposal:" + fmt.Sprint(p.ID),
			Status:      "local", // approved-create lands local; promotion is a separate gate
			Origin:      "proposed",
		})
		if err != nil {
			return nil, err
		}
		res.Version = ver.Version
		res.NewStatus = "local"
	case "promote":
		if err := setStatusTx(ctx, tx, profile, p.SkillName, "promoted"); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO promotion_state (skill_id, promoted_at, promoted_by)
SELECT id, now(), $3 FROM skills WHERE profile=$1 AND name=$2
ON CONFLICT (skill_id) DO UPDATE SET promoted_at=now(), promoted_by=$3, demoted_at=NULL`,
			profile, p.SkillName, decidedBy); err != nil {
			return nil, fmt.Errorf("pgstore: promotion_state: %w", err)
		}
		res.NewStatus = "promoted"
	case "prune":
		if err := setStatusTx(ctx, tx, profile, p.SkillName, "retired"); err != nil {
			return nil, err
		}
		res.NewStatus = "retired"
	default:
		return nil, fmt.Errorf("pgstore: unknown proposal kind %q", p.Kind)
	}

	if _, err := tx.Exec(ctx, `
UPDATE proposals SET status='approved', decided_by=$3, decided_at=now()
WHERE profile=$1 AND id=$2`, profile, id, decidedBy); err != nil {
		return nil, fmt.Errorf("pgstore: mark approved: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgstore: commit approve: %w", err)
	}
	return res, nil
}

// setStatusTx flips a skill's status within a tx; ErrNotFound if absent.
func setStatusTx(ctx context.Context, tx pgx.Tx, profile, name, status string) error {
	tag, err := tx.Exec(ctx, `UPDATE skills SET status=$3 WHERE profile=$1 AND name=$2`, profile, name, status)
	if err != nil {
		return fmt.Errorf("pgstore: set status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// rowScanner abstracts pgx.Row so scanProposalFull works for Query/QueryRow.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProposalFull(row rowScanner) (*Proposal, error) {
	var p Proposal
	var fmRaw, evRaw, vrRaw []byte
	var body, diff, decidedBy, decidedReason *string
	if err := row.Scan(&p.ID, &p.Kind, &p.SkillName, &fmRaw, &body, &diff, &p.Rationale,
		&evRaw, &p.Score, &vrRaw, &p.Status, &p.CreatedBy, &decidedBy, &decidedReason, &p.DecidedAt, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pgstore: scan proposal: %w", err)
	}
	p.ProposedBody = deref(body)
	p.Diff = deref(diff)
	p.DecidedBy = deref(decidedBy)
	p.DecidedReason = deref(decidedReason)
	if len(fmRaw) > 0 {
		_ = json.Unmarshal(fmRaw, &p.ProposedFrontmatter)
	}
	if len(evRaw) > 0 {
		_ = json.Unmarshal(evRaw, &p.Evidence)
	}
	if len(vrRaw) > 0 {
		_ = json.Unmarshal(vrRaw, &p.VerifierReport)
	}
	return &p, nil
}

func jsonOrNil(m map[string]any) []byte {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}
