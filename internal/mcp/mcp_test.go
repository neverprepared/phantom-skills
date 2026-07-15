package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
	"github.com/neverprepared/phantom-skills/internal/config"
)

// fakeDaemon stands up an httptest server mimicking the daemon's skills API.
// received captures the last POST payload per path for assertions.
type fakeDaemon struct {
	srv      *httptest.Server
	received map[string][]byte
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	fd := &fakeDaemon{received: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/skills/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"skills": []map[string]any{
				{"name": "git-flow", "slug": "git-flow", "status": "promoted", "version": 3, "tags": []string{"git"}},
			},
			"next": 1,
		})
	})
	mux.HandleFunc("/api/skills/skills/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/skills/skills/")
		if name != "git-flow" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "NOT_FOUND", "message": "no such skill"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"skill":   map[string]any{"name": "git-flow", "status": "promoted", "version": 3},
			"version": map[string]any{"version": 3, "body": "1. git worktree add", "frontmatter": map[string]any{"description": "Manage worktrees."}},
		})
	})
	mux.HandleFunc("/api/skills/usage", func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		fd.received["usage"] = body
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": 1})
	})
	mux.HandleFunc("/api/skills/proposals", func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		fd.received["proposals"] = body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "status": "pending"})
	})
	fd.srv = httptest.NewServer(mux)
	t.Cleanup(fd.srv.Close)
	return fd
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func newTestServer(t *testing.T, baseURL string) *Server {
	t.Helper()
	c, err := client.New(client.Opts{BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	q, err := wqueue.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return NewServer(ServerDeps{Client: c, Queue: q, Conn: &client.Connectivity{}, Agent: config.Agent{Profile: "personal", Skillset: "default"}})
}

func callTool(t *testing.T, h func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error), args map[string]any) (string, bool) {
	t.Helper()
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty result")
	}
	tc, ok := res.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T", res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestSkillList(t *testing.T) {
	fd := newFakeDaemon(t)
	s := newTestServer(t, fd.srv.URL)
	text, isErr := callTool(t, s.handleSkillList, nil)
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "git-flow") || !strings.Contains(text, "promoted") {
		t.Fatalf("list text missing skill: %s", text)
	}
}

func TestSkillGet(t *testing.T) {
	fd := newFakeDaemon(t)
	s := newTestServer(t, fd.srv.URL)
	text, isErr := callTool(t, s.handleSkillGet, map[string]any{"name": "git-flow"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "git worktree add") || !strings.Contains(text, "Manage worktrees") {
		t.Fatalf("get text missing body/desc: %s", text)
	}

	text, isErr = callTool(t, s.handleSkillGet, map[string]any{"name": "nope"})
	if !isErr {
		t.Fatalf("expected error result for missing skill, got: %s", text)
	}
}

func TestUsageReportValidatesEvent(t *testing.T) {
	fd := newFakeDaemon(t)
	s := newTestServer(t, fd.srv.URL)
	_, isErr := callTool(t, s.handleSkillUsageReport, map[string]any{"skill": "git-flow", "event": "bogus"})
	if !isErr {
		t.Fatal("expected validation error for bad event")
	}
}

func TestUsageReportPostsAndClearsQueue(t *testing.T) {
	fd := newFakeDaemon(t)
	s := newTestServer(t, fd.srv.URL)
	text, isErr := callTool(t, s.handleSkillUsageReport, map[string]any{"skill": "git-flow", "event": "helpful", "note": "worked"})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if strings.Contains(text, "Queued") {
		t.Fatalf("online post should not be queued: %s", text)
	}
	if fd.received["usage"] == nil {
		t.Fatal("daemon did not receive usage payload")
	}
	// Queue should be empty after a successful post.
	if n, _ := s.deps.Queue.Depth(context.Background()); n != 0 {
		t.Fatalf("queue depth after success = %d, want 0", n)
	}
}

func TestProposePostsPayload(t *testing.T) {
	fd := newFakeDaemon(t)
	s := newTestServer(t, fd.srv.URL)
	text, isErr := callTool(t, s.handleSkillPropose, map[string]any{
		"name": "new-skill", "description": "Do a thing. Use when X.", "body": "1. step",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	var got map[string]any
	if err := json.Unmarshal(fd.received["proposals"], &got); err != nil {
		t.Fatalf("proposal payload not JSON: %v", err)
	}
	if got["kind"] != "create" || got["skill_name"] != "new-skill" {
		t.Fatalf("proposal payload wrong: %v", got)
	}
}

func TestOfflineWriteIsQueued(t *testing.T) {
	// Point the client at a dead address so the post fails; the write must be
	// queued (not error) and surface a notice.
	s := newTestServer(t, "http://127.0.0.1:1") // nothing listening
	text, isErr := callTool(t, s.handleSkillUsageReport, map[string]any{"skill": "x", "event": "invoked"})
	if isErr {
		t.Fatalf("offline write should not be a tool error: %s", text)
	}
	if !strings.Contains(text, "Queued") {
		t.Fatalf("offline write should be queued with a notice: %s", text)
	}
	if n, _ := s.deps.Queue.Depth(context.Background()); n != 1 {
		t.Fatalf("queue depth after offline write = %d, want 1", n)
	}
}
