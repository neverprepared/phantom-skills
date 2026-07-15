// Package client is the agent-side HTTP client for the phantom-skills daemon,
// plus the offline write-ahead queue drainer and connectivity tracker. Reads
// (list/get/sync) are online-only and surface a clear error when the daemon is
// unreachable; writes (usage/proposal) are routed through the wqueue so an
// offline session degrades gracefully.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const apiPrefix = "/api/skills"

// Client talks to one daemon over HTTP with a bearer token.
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// Opts configures New.
type Opts struct {
	BaseURL string
	Token   string
	Timeout time.Duration
}

// New constructs a client. BaseURL is the daemon root (e.g. http://host:9997);
// the /api/skills prefix is added per call.
func New(opts Opts) (*Client, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("client: BaseURL is required")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		token:   opts.Token,
		hc:      &http.Client{Timeout: opts.Timeout},
	}, nil
}

// APIError is a non-2xx response decoded from the daemon's error envelope.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("daemon %d %s: %s", e.Status, e.Code, e.Message)
}

// --- wire types (agent-side DTOs mirroring the daemon JSON) ---

// Skill mirrors the daemon's skill identity JSON.
type Skill struct {
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	Origin    string    `json:"origin"`
	Tags      []string  `json:"tags"`
	Version   int       `json:"version"`
	SHA       string    `json:"sha"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillVersion mirrors the daemon's version JSON.
type SkillVersion struct {
	Version     int            `json:"version"`
	SHA         string         `json:"sha"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Body        string         `json:"body,omitempty"`
	Author      string         `json:"author"`
	Source      string         `json:"source"`
	CreatedAt   time.Time      `json:"created_at"`
}

// ListOpts filters ListSkills.
type ListOpts struct {
	Status  string
	Tag     string
	AfterID int64
	Limit   int
}

// ListSkillsResponse is the /skills list payload.
type ListSkillsResponse struct {
	Skills []Skill `json:"skills"`
	Next   int64   `json:"next"`
}

// GetSkillResponse is the single-skill payload.
type GetSkillResponse struct {
	Skill   *Skill        `json:"skill"`
	Version *SkillVersion `json:"version"`
}

// SyncSkill is one promoted skill in the change-feed.
type SyncSkill struct {
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	Status      string         `json:"status"`
	Origin      string         `json:"origin"`
	SHA         string         `json:"sha"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
}

// SyncResponse is the change-feed payload.
type SyncResponse struct {
	Skills  []SyncSkill `json:"skills"`
	Deletes []string    `json:"deletes"`
	Cursor  string      `json:"cursor"`
}

// --- reads (online-only) ---

// Health pings the daemon's unauthenticated health endpoint.
func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, apiPrefix+"/health", nil, nil)
}

// ListSkills returns a page of skills for the caller's scope.
func (c *Client) ListSkills(ctx context.Context, opts ListOpts) (*ListSkillsResponse, error) {
	q := make([]string, 0, 4)
	if opts.Status != "" {
		q = append(q, "status="+opts.Status)
	}
	if opts.Tag != "" {
		q = append(q, "tag="+opts.Tag)
	}
	if opts.AfterID > 0 {
		q = append(q, "after_id="+strconv.FormatInt(opts.AfterID, 10))
	}
	if opts.Limit > 0 {
		q = append(q, "limit="+strconv.Itoa(opts.Limit))
	}
	path := apiPrefix + "/skills"
	if len(q) > 0 {
		path += "?" + strings.Join(q, "&")
	}
	var out ListSkillsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSkill fetches a single skill and its current version.
func (c *Client) GetSkill(ctx context.Context, name string) (*GetSkillResponse, error) {
	var out GetSkillResponse
	if err := c.do(ctx, http.MethodGet, apiPrefix+"/skills/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Sync pulls the change-feed of promoted skills since the given cursor. An
// empty cursor bootstraps a full pull.
func (c *Client) Sync(ctx context.Context, since string) (*SyncResponse, error) {
	path := apiPrefix + "/sync"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	var out SyncResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- writes (routed through the wqueue by callers) ---

// PostUsage POSTs a raw usage payload. Endpoint lands in M4; the wqueue keeps
// items until it exists.
func (c *Client) PostUsage(ctx context.Context, payload []byte) error {
	return c.doRaw(ctx, http.MethodPost, apiPrefix+"/usage", payload, nil)
}

// PostProposal POSTs a raw proposal payload and returns the daemon response.
func (c *Client) PostProposal(ctx context.Context, payload []byte) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.doRaw(ctx, http.MethodPost, apiPrefix+"/proposals", payload, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// do issues a request with a JSON-marshaled body (or nil) and decodes a JSON
// response into out (or discards it).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: marshal body: %w", err)
		}
		raw = b
	}
	return c.doRaw(ctx, method, path, raw, out)
}

// doRaw issues a request with a pre-marshaled body and decodes into out.
func (c *Client) doRaw(ctx context.Context, method, path string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("client: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp.StatusCode, respBody)
	}
	if out != nil && len(respBody) > 0 {
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], respBody...)
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("client: decode response: %w", err)
		}
	}
	return nil
}

func decodeAPIError(status int, body []byte) *APIError {
	e := &APIError{Status: status, Code: "UNKNOWN", Message: strings.TrimSpace(string(body))}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && env.Error.Code != "" {
		e.Code = env.Error.Code
		e.Message = env.Error.Message
	}
	return e
}
