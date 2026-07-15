// Package syncer materializes the shared registry's promoted skills into the
// agent's skills dir (~/.claude/skills/<slug>/SKILL.md), which Claude Code
// file-watches and loads natively. It is the analog of phantom-brain's mart.
//
// Ownership invariant: the syncer only ever writes or removes dirs bearing the
// skillfile ownership marker. A hand-authored skill (no marker) is never
// overwritten or pruned — that is the core safety rule.
package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/skillfile"
)

// Syncer pulls the change-feed and reconciles it against the local skills dir.
type Syncer struct {
	client    *client.Client
	skillsDir string
	stateDir  string
	logger    *slog.Logger
}

// New wires a syncer. skillsDir is where SKILL.md dirs live (~/.claude/skills);
// stateDir is where the sync cursor is persisted.
func New(c *client.Client, skillsDir, stateDir string, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{client: c, skillsDir: skillsDir, stateDir: stateDir, logger: logger}
}

// Options controls one sync run.
type Options struct {
	DryRun bool // report intended changes without writing
	Reset  bool // ignore the saved cursor and pull the full set
}

// Result summarizes a sync run.
type Result struct {
	Written          int
	UpToDate         int
	Deleted          int
	SkippedUnmanaged int
	Cursor           string
	// Changes is a human-readable list of intended actions (populated on DryRun).
	Changes []string
}

// Sync drains the change-feed from the saved cursor (or the beginning when
// Reset) and reconciles each entry into the skills dir. It pages until the
// cursor stops advancing.
func (s *Syncer) Sync(ctx context.Context, opts Options) (Result, error) {
	var res Result
	cursor := ""
	if !opts.Reset {
		cursor = s.loadCursor()
	}

	for {
		resp, err := s.client.Sync(ctx, cursor)
		if err != nil {
			return res, err
		}
		for _, sk := range resp.Skills {
			if err := s.materialize(sk, opts.DryRun, &res); err != nil {
				return res, err
			}
		}
		for _, name := range resp.Deletes {
			if err := s.remove(name, opts.DryRun, &res); err != nil {
				return res, err
			}
		}
		if resp.Cursor == "" || resp.Cursor == cursor {
			break // no forward progress ⇒ caught up
		}
		cursor = resp.Cursor
	}

	res.Cursor = cursor
	if !opts.DryRun {
		if err := s.saveCursor(cursor); err != nil {
			return res, err
		}
	}
	return res, nil
}

// materialize writes one promoted skill's SKILL.md, honoring the ownership
// invariant and skipping writes when the on-disk content is already current.
func (s *Syncer) materialize(sk client.SyncSkill, dryRun bool, res *Result) error {
	slug := sk.Slug
	if slug == "" {
		slug = skillfile.Slugify(sk.Name)
	}
	dir := filepath.Join(s.skillsDir, slug)
	path := filepath.Join(dir, "SKILL.md")

	if existing, err := os.ReadFile(path); err == nil {
		fm, _, perr := skillfile.Parse(existing)
		if perr == nil {
			marker, managed := skillfile.MarkerOf(fm)
			if !managed {
				res.SkippedUnmanaged++
				s.logger.Warn("phantom-skills: skipping unmanaged skill dir (no marker)", slog.String("slug", slug))
				return nil
			}
			if marker.SHA == sk.SHA {
				res.UpToDate++
				return nil // already current
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("syncer: stat %s: %w", path, err)
	}

	content, err := skillfile.Render(sk.Frontmatter, sk.Body, skillfile.Marker{
		Origin: sk.Origin, Status: sk.Status, SHA: sk.SHA,
	})
	if err != nil {
		return err
	}
	if dryRun {
		res.Changes = append(res.Changes, "write "+slug)
		res.Written++
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("syncer: mkdir %s: %w", dir, err)
	}
	if err := writeAtomic(path, content); err != nil {
		return err
	}
	res.Written++
	return nil
}

// remove deletes a managed skill dir for a retired skill. Unmanaged dirs are
// left untouched.
func (s *Syncer) remove(name string, dryRun bool, res *Result) error {
	slug := skillfile.Slugify(name)
	dir := filepath.Join(s.skillsDir, slug)
	path := filepath.Join(dir, "SKILL.md")
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("syncer: stat %s: %w", path, err)
	}
	fm, _, perr := skillfile.Parse(existing)
	if perr != nil {
		return nil
	}
	if _, managed := skillfile.MarkerOf(fm); !managed {
		res.SkippedUnmanaged++
		return nil // never remove a hand-authored dir
	}
	if dryRun {
		res.Changes = append(res.Changes, "delete "+slug)
		res.Deleted++
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("syncer: remove %s: %w", dir, err)
	}
	res.Deleted++
	return nil
}

func (s *Syncer) cursorPath() string { return filepath.Join(s.stateDir, "sync.cursor") }

func (s *Syncer) loadCursor() string {
	b, err := os.ReadFile(s.cursorPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Syncer) saveCursor(cursor string) error {
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		return fmt.Errorf("syncer: mkdir state dir: %w", err)
	}
	return writeAtomic(s.cursorPath(), []byte(cursor+"\n"))
}

// writeAtomic writes via a temp file + rename so a reader never sees a partial
// SKILL.md (Claude Code is watching).
func writeAtomic(path string, content []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("syncer: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("syncer: rename %s: %w", path, err)
	}
	return nil
}
