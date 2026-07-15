// Package llm is a thin Claude API wrapper for the intelligence pipeline. It
// centralizes model-tier selection and the Messages call so the pipeline
// stages (author, verify, score) don't each re-implement HTTP.
//
// SCAFFOLDING (M5): the client compiles and can make a basic Messages call, but
// no pipeline stage calls it yet. Before M6 wires real authoring/verification,
// CONFIRM the model IDs and request shape via the `claude-api` skill — do not
// trust the constants below blindly, and add structured-output (json_schema)
// and adaptive-thinking support there.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Model tiers. Cheap for high-volume clustering/scoring; strong for anything
// that lands IN a skill (authoring, the verify critic, description tuning).
// Confirm these IDs via the claude-api skill before M6.
const (
	ModelCheap  = "claude-haiku-4-5-20251001"
	ModelMid    = "claude-sonnet-5"
	ModelStrong = "claude-opus-4-8"
)

const (
	apiURL         = "https://api.anthropic.com/v1/messages"
	anthropicVer   = "2023-06-01"
	defaultTimeout = 120 * time.Second
)

// Client calls the Anthropic Messages API.
type Client struct {
	apiKey string
	hc     *http.Client
}

// New builds a client. apiKey empty ⇒ falls back to ANTHROPIC_API_KEY.
func New(apiKey string) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	return &Client{apiKey: apiKey, hc: &http.Client{Timeout: defaultTimeout}}
}

// Configured reports whether an API key is available.
func (c *Client) Configured() bool { return c.apiKey != "" }

// Request is a minimal Messages request. M6 extends this with system prompts,
// structured output (output_config json_schema), and thinking config.
type Request struct {
	Model     string
	MaxTokens int
	Prompt    string
}

// Complete sends a single-user-turn Messages request and returns the
// concatenated text content.
func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("llm: no ANTHROPIC_API_KEY configured")
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 2048
	}
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   []map[string]any{{"role": "user", "content": req.Prompt}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("llm: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVer)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm: request: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := "unknown"
		if out.Error != nil {
			msg = out.Error.Message
		}
		return "", fmt.Errorf("llm: api %d: %s", resp.StatusCode, msg)
	}
	var text string
	for _, block := range out.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return text, nil
}
