package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/skillfile"
)

// feedServer models a single-generation change-feed at `cursor`: any client
// whose `since` != cursor receives the full (skills, deletes) set; a caught-up
// client (since == cursor) receives an empty page. This mirrors the real
// daemon closely enough to exercise cursor advance, idempotent re-sync, and
// delete propagation.
func feedServer(t *testing.T, skills, deletes any, cursor string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		page := map[string]any{"skills": []any{}, "deletes": []any{}, "cursor": cursor}
		if since != cursor {
			page["skills"] = skills
			page["deletes"] = deletes
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newSyncer(t *testing.T, baseURL, skillsDir string) *Syncer {
	t.Helper()
	c, err := client.New(client.Opts{BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	return New(c, skillsDir, t.TempDir(), nil)
}

func TestMaterializeWritesManagedSkill(t *testing.T) {
	skillsDir := t.TempDir()
	srv := feedServer(t, []map[string]any{{
		"name": "git-flow", "slug": "git-flow", "status": "promoted", "origin": "authored",
		"sha": "sha-v1", "frontmatter": map[string]any{"name": "git-flow", "description": "d"}, "body": "1. do it",
	}}, []any{}, "100.1")
	sy := newSyncer(t, srv.URL, skillsDir)
	res, err := sy.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Written != 1 {
		t.Fatalf("written=%d want 1", res.Written)
	}
	data, err := os.ReadFile(filepath.Join(skillsDir, "git-flow", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	fm, body, _ := skillfile.Parse(data)
	if fm["description"] != "d" || body == "" {
		t.Fatalf("bad rendered file: fm=%v body=%q", fm, body)
	}
	m, ok := skillfile.MarkerOf(fm)
	if !ok || m.SHA != "sha-v1" {
		t.Fatalf("marker missing/wrong: %+v", m)
	}

	// Second run with the same sha should be a no-op (up to date).
	sy2 := newSyncer(t, srv.URL, skillsDir)
	res2, err := sy2.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Written != 0 || res2.UpToDate != 1 {
		t.Fatalf("idempotent re-sync: written=%d uptodate=%d", res2.Written, res2.UpToDate)
	}
}

func TestNeverOverwritesUnmanaged(t *testing.T) {
	skillsDir := t.TempDir()
	// Pre-place a hand-authored skill (no marker) at the same slug.
	dir := filepath.Join(skillsDir, "git-flow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "---\nname: git-flow\ndescription: MINE\n---\n\nhand written\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := feedServer(t, []map[string]any{{
		"name": "git-flow", "slug": "git-flow", "status": "promoted",
		"sha": "sha-x", "frontmatter": map[string]any{"description": "REGISTRY"}, "body": "registry body",
	}}, []any{}, "100.1")
	sy := newSyncer(t, srv.URL, skillsDir)
	res, err := sy.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.SkippedUnmanaged != 1 || res.Written != 0 {
		t.Fatalf("expected skip-unmanaged, got written=%d skipped=%d", res.Written, res.SkippedUnmanaged)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if string(data) != original {
		t.Fatalf("hand-authored file was modified:\n%s", data)
	}
}

func TestDeleteRemovesManagedOnly(t *testing.T) {
	skillsDir := t.TempDir()

	// One managed skill (will be deleted) and one hand-authored (must survive).
	managed := filepath.Join(skillsDir, "gone")
	_ = os.MkdirAll(managed, 0o755)
	m, _ := skillfile.Render(map[string]any{"name": "gone"}, "b", skillfile.Marker{Origin: "authored", Status: "promoted", SHA: "s"})
	_ = os.WriteFile(filepath.Join(managed, "SKILL.md"), m, 0o644)

	hand := filepath.Join(skillsDir, "keep")
	_ = os.MkdirAll(hand, 0o755)
	_ = os.WriteFile(filepath.Join(hand, "SKILL.md"), []byte("---\nname: keep\n---\nx"), 0o644)

	srv := feedServer(t, []any{}, []string{"gone", "keep"}, "200.5") // registry says both retired
	sy := newSyncer(t, srv.URL, skillsDir)
	res, err := sy.Sync(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("deleted=%d want 1 (managed only)", res.Deleted)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatal("managed dir should be removed")
	}
	if _, err := os.Stat(hand); err != nil {
		t.Fatal("hand-authored dir must survive delete")
	}
}
