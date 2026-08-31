package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/neverprepared/phantom-skills/internal/pgstore"
)

// startWorkers launches the background pipeline loop. It runs until ctx is
// cancelled. With the zero-value Pipeline (no algorithms wired) each tick is a
// cheap no-op, so this is safe to always start.
func (d *Daemon) startWorkers(ctx context.Context) {
	interval := time.Duration(d.cfg.Pipeline.DetectorPollIntervalSecs) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		last := time.Unix(0, 0)
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if err := d.runPipelineOnce(ctx, last); err != nil {
					d.Logger.Warn("phantom-skills: pipeline tick failed", slog.String("err", err.Error()))
				}
				last = now
			}
		}
	}()
}

// runPipelineOnce drives one pass of the seam: Detect → (Author → Verify) →
// insert pending proposals; Prune → insert pending proposals. Every proposal
// lands in the queue as 'pending' — nothing is applied without a human (or
// autonomous opt-in) approval. Nil pipeline members short-circuit.
func (d *Daemon) runPipelineOnce(ctx context.Context, since time.Time) error {
	if d.store == nil || d.pipeline.Detector == nil {
		return nil // nothing to do until storage + a detector are wired
	}
	candidates, err := d.pipeline.Detector.Detect(ctx, since)
	if err != nil {
		return err
	}
	optionB := d.bbox != nil && d.cfg.Registry.RepoURL != ""
	for _, c := range candidates {
		if optionB {
			d.dispatchRatchet(ctx, c)
			continue
		}
		// Legacy path: author/verify → pending proposal (human gate in Postgres).
		if err := d.proposeCandidate(ctx, c); err != nil {
			d.Logger.Warn("phantom-skills: propose candidate failed",
				slog.String("skill", c.SkillName), slog.String("err", err.Error()))
		}
	}
	return nil
}

// dispatchRatchet fires an auto-fire ratchet for a candidate under each
// configured scope (single-scope default for now), logging the outcome and
// recording fired ratchets to phantom-brain.
func (d *Daemon) dispatchRatchet(ctx context.Context, c Candidate) {
	for _, b := range d.registry.Scopes() {
		res, err := d.autoFireRatchet(ctx, b.Key.Profile, c)
		switch {
		case err != nil:
			d.Logger.Warn("phantom-skills: auto-fire failed",
				slog.String("skill", c.SkillName), slog.String("err", err.Error()))
		case res.Fired:
			d.Logger.Info("phantom-skills: ratchet fired",
				slog.String("skill", c.SkillName), slog.String("kind", c.Kind), slog.String("task", res.TaskID))
			if d.brain != nil {
				d.brain.RecordDecision(ctx, "fired", c.Kind, c.SkillName, "pipeline")
			}
		default:
			d.Logger.Debug("phantom-skills: ratchet skipped",
				slog.String("skill", c.SkillName), slog.String("reason", res.Reason))
		}
		break // single-scope default; M10 fans out per scope
	}
}

// proposeCandidate authors + verifies a create candidate (if an Author/Verifier
// are wired), then records it as a pending proposal. prune/promote candidates
// are recorded directly.
func (d *Daemon) proposeCandidate(ctx context.Context, c Candidate) error {
	in := pgstore.ProposalInput{
		Kind:                c.Kind,
		SkillName:           c.SkillName,
		ProposedFrontmatter: c.Frontmatter,
		ProposedBody:        c.Body,
		Rationale:           c.Rationale,
		Evidence:            c.Evidence,
		CreatedBy:           "pipeline",
	}
	if c.Score != 0 {
		in.Score = &c.Score
	}

	if c.Kind == "create" {
		if d.pipeline.Author != nil && in.ProposedBody == "" {
			fm, body, err := d.pipeline.Author.Author(ctx, c)
			if err != nil {
				return err
			}
			in.ProposedFrontmatter, in.ProposedBody = fm, body
		}
		if d.pipeline.Verifier != nil {
			report, err := d.pipeline.Verifier.Verify(ctx, c.SkillName, in.ProposedFrontmatter, in.ProposedBody)
			if err != nil {
				return err
			}
			in.VerifierReport = map[string]any{
				"score": report.Score, "passed": report.Passed, "findings": report.Findings,
			}
			if !report.Passed {
				return nil // failed the retention gate — don't surface a proposal
			}
		}
	}

	// The profile a pipeline-generated proposal belongs to: the daemon serves
	// per-scope, so pipeline candidates are recorded under each configured
	// scope's profile. For M5 the loop runs per default scope.
	for _, b := range d.registry.Scopes() {
		in.Profile = b.Key.Profile
		if _, err := d.store.CreateProposal(ctx, in); err != nil {
			return err
		}
		break // single-scope default; M10 fans out per scope
	}
	return nil
}
