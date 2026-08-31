package brainbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockBrainbox mimics the brainbox API surface the client uses.
func mockBrainbox(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var creates []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/key", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"key": "test-key"})
	})
	mux.HandleFunc("/api/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		creates = append(creates, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": body["name"], "url": "https://sandbox.example/" + body["name"].(string), "backend": body["backend"],
		})
	})
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"response": "authored the skill"})
	})
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	})
	mux.HandleFunc("/api/ratchet", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		creates = append(creates, body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "job_id": "job-1", "task_id": "task-1", "repo_url": body["repo_url"],
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &creates
}

func TestAuthKeyFetchedLazily(t *testing.T) {
	srv, _ := mockBrainbox(t)
	c := New(srv.URL, "")
	if _, err := c.key(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.apiKey != "test-key" {
		t.Fatalf("key not cached: %q", c.apiKey)
	}
}

func TestRunWorkerSendsTopLevelTask(t *testing.T) {
	srv, creates := mockBrainbox(t)
	c := New(srv.URL, "")
	resp, err := c.RunWorker(context.Background(), WorkerSpec{
		Name:    "improve-git-worktree-flow",
		Task:    "clone the registry, tighten git-worktree-flow's description, open a PR",
		Profile: "personal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp["name"] != "improve-git-worktree-flow" {
		t.Fatalf("unexpected create response: %v", resp)
	}
	if len(*creates) != 1 {
		t.Fatalf("expected 1 create, got %d", len(*creates))
	}
	got := (*creates)[0]
	if got["role"] != "worker" {
		t.Fatalf("role = %v, want worker", got["role"])
	}
	// The task MUST be top-level (body.task), not nested in a repo object — the
	// live API drops unknown `repo` fields and reads body.task.
	if task, _ := got["task"].(string); task == "" {
		t.Fatalf("top-level task not sent: %v", got)
	}
	if _, hasRepo := got["repo"]; hasRepo {
		t.Fatalf("client must not send a repo object (unsupported): %v", got)
	}
}

func TestRunWorkerRequiresTask(t *testing.T) {
	srv, _ := mockBrainbox(t)
	c := New(srv.URL, "")
	if _, err := c.RunWorker(context.Background(), WorkerSpec{Name: "x"}); err == nil {
		t.Fatal("expected error for empty task")
	}
}

func TestRatchetSendsRepoURLAndTask(t *testing.T) {
	srv, creates := mockBrainbox(t)
	c := New(srv.URL, "")
	res, err := c.Ratchet(context.Background(), RatchetSpec{
		RepoURL: "git@github.com:neverprepared/phantom-skills-registry",
		Task:    "improve git-worktree-flow; open a PR",
		Branch:  "skill/improve-git-worktree-flow",
		Profile: "personal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.TaskID != "task-1" {
		t.Fatalf("unexpected ratchet result: %+v", res)
	}
	got := (*creates)[len(*creates)-1]
	if got["repo_url"] == "" || got["task"] == "" {
		t.Fatalf("ratchet must send repo_url + task: %v", got)
	}
	if got["branch"] != "skill/improve-git-worktree-flow" || got["workspace_profile"] != "personal" {
		t.Fatalf("ratchet optional fields wrong: %v", got)
	}
}

func TestRatchetRequiresRepoAndTask(t *testing.T) {
	srv, _ := mockBrainbox(t)
	c := New(srv.URL, "")
	if _, err := c.Ratchet(context.Background(), RatchetSpec{Task: "x"}); err == nil {
		t.Fatal("expected error without repo_url")
	}
}

func TestQueryExtractsResponse(t *testing.T) {
	srv, _ := mockBrainbox(t)
	c := New(srv.URL, "")
	out, err := c.Query(context.Background(), "sess", "author this", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out != "authored the skill" {
		t.Fatalf("query out = %q", out)
	}
}

func TestApiErrorSurfaced(t *testing.T) {
	// A server that 500s on create — client must surface it, not swallow.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/key" {
			_ = json.NewEncoder(w).Encode(map[string]any{"key": "k"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()
	c := New(srv.URL, "")
	if _, err := c.Create(context.Background(), CreateRequest{Name: "x"}); err == nil {
		t.Fatal("expected error from 500")
	}
}
