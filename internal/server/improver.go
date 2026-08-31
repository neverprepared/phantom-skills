package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neverprepared/phantom-skills/internal/brainbox"
	"github.com/neverprepared/phantom-skills/internal/pgstore"
	"github.com/neverprepared/phantom-skills/internal/skillfile"
)

// FireResult reports what auto-fire did for a candidate.
type FireResult struct {
	Fired  bool
	Reason string // why skipped (when !Fired) or "fired"
	TaskID string
}

// autoFireRatchet is the Option B improvement path: turn a detected Candidate
// into a brainbox ratchet that clones the registry, makes the change, and opens
// a PR (the gate). Guards enforce the anti-nag/rate-limit contract so a noisy
// detector can't spam PRs or stampede containers.
//
// Returns Fired=false with a Reason (not an error) when a guard suppresses the
// fire; errors are reserved for real failures (store/brainbox).
func (d *Daemon) autoFireRatchet(ctx context.Context, profile string, c Candidate) (FireResult, error) {
	if d.bbox == nil || strings.TrimSpace(d.cfg.Registry.RepoURL) == "" {
		return FireResult{Reason: "auto-fire not configured"}, nil
	}
	if d.store == nil {
		return FireResult{Reason: "no store"}, nil
	}
	skill := c.SkillName

	// Dedup: one active ratchet per skill.
	if inflight, err := d.store.HasInFlightRatchet(ctx, profile, skill); err != nil {
		return FireResult{}, err
	} else if inflight {
		return FireResult{Reason: "in-flight ratchet exists"}, nil
	}
	// Anti-nag: cooldown window after any prior run (incl. a human-closed PR).
	cooldown := time.Duration(d.cfg.Registry.RatchetCooldownHours) * time.Hour
	if cooldown > 0 {
		if recent, err := d.store.RecentlyRatcheted(ctx, profile, skill, cooldown); err != nil {
			return FireResult{}, err
		} else if recent {
			return FireResult{Reason: "within cooldown"}, nil
		}
	}
	// Rate-limit: cap concurrent ratchets across the profile.
	if max := d.cfg.Registry.MaxConcurrentRatchets; max > 0 {
		if n, err := d.store.CountInFlightRatchets(ctx, profile); err != nil {
			return FireResult{}, err
		} else if n >= max {
			return FireResult{Reason: "rate-limited"}, nil
		}
	}

	branch := fmt.Sprintf("skill/%s-%s", c.Kind, skillfile.Slugify(skill))
	res, err := d.bbox.Ratchet(ctx, brainbox.RatchetSpec{
		RepoURL: d.cfg.Registry.RepoURL,
		Task:    composeRatchetTask(c),
		Branch:  branch,
		Profile: profile,
	})
	if err != nil {
		return FireResult{}, fmt.Errorf("fire ratchet: %w", err)
	}
	if _, err := d.store.RecordRatchet(ctx, profile, pgstore.RatchetRun{
		SkillName: skill, Kind: c.Kind, TaskID: res.TaskID, JobID: res.JobID,
		Branch: branch, Status: "fired", Rationale: c.Rationale,
	}); err != nil {
		// The ratchet is already running; failing to ledger it is a real problem
		// (breaks dedup), so surface it.
		return FireResult{}, fmt.Errorf("record ratchet: %w", err)
	}
	return FireResult{Fired: true, Reason: "fired", TaskID: res.TaskID}, nil
}

// composeRatchetTask writes the worker's instructions. The ratchet worker role
// already clones the repo (BRAINBOX_REPO_URL), opens a PR, and drives CI to
// green — so this is purely the WHAT: the change to make and the invariants to
// keep. The lint gate mirrors the registry's CI.
func composeRatchetTask(c Candidate) string {
	slug := skillfile.Slugify(c.SkillName)
	path := "skills/" + slug + "/SKILL.md"
	invariants := "Keep the format valid: frontmatter starts at byte 0; `name` equals the directory " +
		slug + " (^[a-z0-9-]+$, ≤64 chars); `description` states WHAT it does AND WHEN to use it (≤1024 " +
		"chars — this is the trigger); include the `x-phantom-skills` marker; body <500 lines. " +
		"Run `python3 .github/lint_skills.py` and ensure it prints OK before opening the PR."

	var b strings.Builder
	switch c.Kind {
	case "create":
		fmt.Fprintf(&b, "Author a new skill at %s.\n\nWhat it should capture: %s\n\n", path, c.Rationale)
	case "edit":
		fmt.Fprintf(&b, "Improve the existing skill at %s.\n\nWhat needs fixing: %s\n\n", path, c.Rationale)
	case "prune":
		fmt.Fprintf(&b, "Retire the stale/superseded skill %s: delete its directory skills/%s/.\n\nWhy: %s\n\n",
			c.SkillName, slug, c.Rationale)
	case "merge":
		fmt.Fprintf(&b, "Merge near-duplicate skills into %s (fold the others' triggers into its description, "+
			"then delete the redundant directories).\n\nWhy: %s\n\n", path, c.Rationale)
	default:
		fmt.Fprintf(&b, "Update the skill at %s.\n\n%s\n\n", path, c.Rationale)
	}
	if c.Kind != "prune" {
		b.WriteString(invariants + "\n")
	}
	if len(c.Evidence) > 0 {
		fmt.Fprintf(&b, "\nEvidence: %v\n", c.Evidence)
	}
	b.WriteString("\nOpen a PR titled \"skill: " + c.Kind + " " + c.SkillName + "\" with the evidence in the body.")
	return b.String()
}
