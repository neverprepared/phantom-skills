// Package brainbox is the client for the brainbox API — the phantom fleet's
// shared agent-execution + sandbox plane. brainbox spins up Claude Code agents
// in Docker/UTM sandboxes (authenticated via the Claude subscription, NOT an
// API key), which is how phantom-skills does its agentic work: authoring and
// verifying skills without ever holding an ANTHROPIC_API_KEY.
//
// The daemon's improvement loop fires a *ratchet* — an autonomous ci-ratchet
// worker that clones the skills registry, authors/improves a SKILL.md, verifies
// it in a sub-sandbox, and opens a PR. The PR is the human gate.
package brainbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the brainbox API.
type Client struct {
	baseURL string
	apiKey  string // fetched lazily from /api/auth/key when empty
	hc      *http.Client
}

// New builds a client. baseURL is the brainbox root (e.g.
// https://brainbox-api.neverprepared.com). apiKey may be empty — it's fetched
// from /api/auth/key on first use.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: 6 * time.Minute}, // ratchet create + long query
	}
}

// key returns the API key, fetching it once from /api/auth/key if not set.
func (c *Client) key(ctx context.Context) (string, error) {
	if c.apiKey != "" {
		return c.apiKey, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/auth/key", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("brainbox: fetch auth key: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("brainbox: decode auth key: %w", err)
	}
	if out.Key == "" {
		return "", fmt.Errorf("brainbox: empty auth key")
	}
	c.apiKey = out.Key
	return c.apiKey, nil
}

// RepoSpec configures repo access for a session.
type RepoSpec struct {
	URL             string `json:"url"`
	Mode            string `json:"mode"` // worktree-mount | clone | clone-worktree | ci-ratchet
	Branch          string `json:"branch,omitempty"`
	ContainerPath   string `json:"container_path,omitempty"`
	Task            string `json:"task,omitempty"`              // ci-ratchet
	StartMergeQueue bool   `json:"start_merge_queue,omitempty"` // ci-ratchet
}

// CreateRequest is the POST /api/create payload.
type CreateRequest struct {
	Name             string    `json:"name"`
	Role             string    `json:"role"`    // developer|supervisor|worker|reviewer|merge-queue|pr-shepherd
	Backend          string    `json:"backend"` // docker (default) | utm
	WorkspaceProfile string    `json:"workspace_profile,omitempty"`
	WorkspaceHome    string    `json:"workspace_home,omitempty"`
	Repo             *RepoSpec `json:"repo,omitempty"`
}

// Create spins up a sandboxed session and returns the raw response (fields vary
// by backend; callers pull what they need — e.g. "url", "name").
func (c *Client) Create(ctx context.Context, req CreateRequest) (map[string]any, error) {
	if req.Backend == "" {
		req.Backend = "docker"
	}
	var out map[string]any
	if err := c.do(ctx, http.MethodPost, "/api/create", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Query sends a prompt to a running session and returns its response text.
// timeout is the in-container agent timeout (seconds are sent to brainbox).
func (c *Client) Query(ctx context.Context, session, prompt string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	body := map[string]any{"prompt": prompt, "timeout": int(timeout.Seconds())}
	var out map[string]any
	if err := c.do(ctx, http.MethodPost, "/api/sessions/"+session+"/query", body, &out); err != nil {
		return "", err
	}
	// brainbox returns the agent's reply under one of these keys depending on
	// version; fall back to the whole payload so nothing is silently dropped.
	for _, k := range []string{"response", "output", "result", "text"} {
		if v, ok := out[k].(string); ok && v != "" {
			return v, nil
		}
	}
	raw, _ := json.Marshal(out)
	return string(raw), nil
}

// Delete tears down a session.
func (c *Client) Delete(ctx context.Context, session string) error {
	return c.do(ctx, http.MethodPost, "/api/delete", map[string]any{"name": session}, nil)
}

// RatchetSpec fires an autonomous ci-ratchet worker: clone repo → do task →
// open PR → (CI/merge-queue gates) → auto-delete.
type RatchetSpec struct {
	Name            string
	RepoURL         string
	Branch          string // default work/<name> server-side
	Task            string
	Backend         string // default docker
	StartMergeQueue bool
	Profile         string
}

// FireRatchet launches a ci-ratchet worker for RatchetSpec and returns the raw
// create response.
func (c *Client) FireRatchet(ctx context.Context, s RatchetSpec) (map[string]any, error) {
	if strings.TrimSpace(s.RepoURL) == "" || strings.TrimSpace(s.Task) == "" {
		return nil, fmt.Errorf("brainbox: ratchet requires RepoURL and Task")
	}
	return c.Create(ctx, CreateRequest{
		Name:             s.Name,
		Role:             "worker",
		Backend:          s.Backend,
		WorkspaceProfile: s.Profile,
		Repo: &RepoSpec{
			URL:             s.RepoURL,
			Mode:            "ci-ratchet",
			Branch:          s.Branch,
			ContainerPath:   "/home/developer/workspace/repo",
			Task:            s.Task,
			StartMergeQueue: s.StartMergeQueue,
		},
	})
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	key, err := c.key(ctx)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("brainbox: marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("brainbox: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", key)

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("brainbox: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("brainbox: %s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("brainbox: decode response: %w", err)
		}
	}
	return nil
}
