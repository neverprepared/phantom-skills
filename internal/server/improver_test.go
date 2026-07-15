package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/neverprepared/phantom-skills/internal/brainbox"
	"github.com/neverprepared/phantom-skills/internal/pgstore"
)

func TestComposeRatchetTask(t *testing.T) {
	edit := composeRatchetTask(Candidate{Kind: "edit", SkillName: "git-worktree-flow", Rationale: "too broad; mis-triggers"})
	if !strings.Contains(edit, "skills/git-worktree-flow/SKILL.md") ||
		!strings.Contains(edit, "Improve the existing skill") ||
		!strings.Contains(edit, "lint_skills.py") ||
		!strings.Contains(edit, `skill: edit git-worktree-flow`) {
		t.Fatalf("edit task missing expected content:\n%s", edit)
	}
	create := composeRatchetTask(Candidate{Kind: "create", SkillName: "pdf-extract", Rationale: "recurred"})
	if !strings.Contains(create, "Author a new skill") {
		t.Fatalf("create task wrong:\n%s", create)
	}
	prune := composeRatchetTask(Candidate{Kind: "prune", SkillName: "old", Rationale: "stale"})
	if !strings.Contains(prune, "Retire") || !strings.Contains(prune, "delete its directory") {
		t.Fatalf("prune task wrong:\n%s", prune)
	}
}

// mockRatchetBrainbox returns an httptest server accepting /api/ratchet, and a
// pointer to the count of ratchets fired.
func mockRatchetBrainbox(t *testing.T, fired *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ratchet" {
			*fired++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "task_id": "t1", "job_id": "j1"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAutoFireRatchetGuards(t *testing.T) {
	dsn := os.Getenv("PSKILLS_TEST_DSN")
	if dsn == "" {
		t.Skip("PSKILLS_TEST_DSN not set")
	}
	if err := pgstore.Migrate(dsn); err != nil {
		t.Fatal(err)
	}
	store, err := pgstore.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	fired := 0
	bb := mockRatchetBrainbox(t, &fired)

	cfg := &ServerConfig{}
	cfg.Registry.RepoURL = "git@github.com:neverprepared/phantom-skills-registry"
	cfg.Registry.MaxConcurrentRatchets = 100 // high so leftover ledger rows can't gate this isolated fire
	cfg.Registry.RatchetCooldownHours = 24
	d := &Daemon{
		cfg:    cfg,
		store:  store,
		bbox:   brainbox.New(bb.URL, "k"),
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	ctx := context.Background()
	// Unique per run so a prior run's in-flight ledger row can't suppress this
	// fire (the ledger persists across test runs against the same database).
	skill := fmt.Sprintf("svc-guardtest-%d", time.Now().UnixNano())

	res, err := d.autoFireRatchet(ctx, "personal", Candidate{Kind: "edit", SkillName: skill, Rationale: "x"})
	if err != nil {
		t.Fatalf("fire: %v", err)
	}
	if !res.Fired || res.TaskID != "t1" {
		t.Fatalf("expected fired with task t1, got %+v", res)
	}
	if fired != 1 {
		t.Fatalf("brainbox fired %d times, want 1", fired)
	}

	// Second attempt for the same skill → suppressed by the in-flight guard.
	res2, err := d.autoFireRatchet(ctx, "personal", Candidate{Kind: "edit", SkillName: skill, Rationale: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Fired || !strings.Contains(res2.Reason, "in-flight") {
		t.Fatalf("expected in-flight suppression, got %+v", res2)
	}
	if fired != 1 {
		t.Fatalf("guard should have prevented a 2nd fire; fired=%d", fired)
	}

	// Not configured → clean skip, no error.
	d.bbox = nil
	res3, err := d.autoFireRatchet(ctx, "personal", Candidate{Kind: "edit", SkillName: "anything", Rationale: "x"})
	if err != nil || res3.Fired {
		t.Fatalf("unconfigured should skip cleanly: %+v %v", res3, err)
	}
}
