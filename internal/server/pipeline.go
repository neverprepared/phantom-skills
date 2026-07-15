package server

import (
	"context"
	"time"
)

// This file is THE SEAM. The plumbing above (API, storage, sync, auth, the
// worker loop below) knows nothing about the intelligence algorithms behind
// these interfaces. internal/pipeline implements them (M6+); until then the
// daemon runs with the zero-value Pipeline (all nil members = no-ops).

// Signal is a normalized usage/telemetry observation the pipeline consumes.
type Signal struct {
	SkillName string
	SessionID string
	Machine   string
	Event     string
	Context   map[string]any
	TS        time.Time
}

// Candidate is a create/prune/promote idea emitted by a Detector or Pruner.
// For a create candidate, Frontmatter/Body may be empty (the Author fills
// them) or pre-populated (an agent-submitted proposal).
type Candidate struct {
	Kind        string // create|prune|promote
	SkillName   string
	Rationale   string
	Frontmatter map[string]any
	Body        string
	Evidence    map[string]any
	Score       float64
}

// VerifierReport is the retention-gate verdict for a proposed skill.
type VerifierReport struct {
	Score    float64
	Passed   bool
	Findings []string
}

// SkillSummary is the minimal view of an existing skill the Pruner/Promoter need.
type SkillSummary struct {
	Name    string
	Status  string
	Version int
}

// Detector watches usage + brain and emits create/prune/promote Candidates.
type Detector interface {
	Detect(ctx context.Context, since time.Time) ([]Candidate, error)
}

// Author turns a create/edit Candidate into concrete SKILL.md content.
type Author interface {
	Author(ctx context.Context, c Candidate) (frontmatter map[string]any, body string, err error)
}

// Verifier scores/validates a proposed skill before it can be gated.
type Verifier interface {
	Verify(ctx context.Context, name string, frontmatter map[string]any, body string) (VerifierReport, error)
}

// Pruner decides which existing skills are stale/redundant.
type Pruner interface {
	Prune(ctx context.Context, skills []SkillSummary, signals []Signal) ([]Candidate, error)
}

// Promoter decides which local/draft skills earn fleet-wide promotion.
type Promoter interface {
	Promote(ctx context.Context, skill SkillSummary, signals []Signal) (promote bool, score float64, err error)
}

// Pipeline bundles the stages. A nil member is treated as a no-op by the worker
// loop, so the daemon runs end-to-end before any algorithm lands.
type Pipeline struct {
	Detector
	Author
	Verifier
	Pruner
	Promoter
}
