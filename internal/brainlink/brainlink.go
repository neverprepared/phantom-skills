// Package brainlink records phantom-skills decisions back into phantom-brain's
// long-term memory over its HTTP API. Every write is best-effort: a brain
// outage must never fail a skills operation, so errors are logged and
// swallowed. The exact learn payload is provisional and refined when wired
// against a live brain daemon.
package brainlink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config mirrors the [brain] block in server.toml.
type Config struct {
	API             string
	Token           string
	Profile         string
	Vault           string
	RecordLearnings bool
	RecordTelemetry bool
}

// Client posts learnings/telemetry to phantom-brain.
type Client struct {
	cfg    Config
	hc     *http.Client
	logger *slog.Logger
}

// New returns a client, or nil when no brain API is configured (callers
// nil-check before use).
func New(cfg Config, logger *slog.Logger) *Client {
	if strings.TrimSpace(cfg.API) == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		cfg:    cfg,
		hc:     &http.Client{Timeout: 10 * time.Second},
		logger: logger,
	}
}

// RecordDecision records an applied create/prune/promote decision as an
// episodic finding. Best-effort; never returns an error.
func (c *Client) RecordDecision(ctx context.Context, decision, kind, skillName, by string) {
	if c == nil || !c.cfg.RecordLearnings {
		return
	}
	title := fmt.Sprintf("skill %s %s: %s", kind, decision, skillName)
	body := fmt.Sprintf("phantom-skills %s a %s proposal for skill %q (by %s).", decision, kind, skillName, by)
	c.learn(ctx, learnRequest{
		Title:      title,
		RawBody:    body,
		Tags:       []string{"phantom-skills", kind, decision},
		MemoryType: "episodic",
		Source:     []string{"phantom-skills"},
	})
}

// learnRequest is the provisional shape posted to POST /api/brain/learn.
type learnRequest struct {
	Title      string   `json:"title"`
	RawBody    string   `json:"raw_body"`
	Tags       []string `json:"tags"`
	MemoryType string   `json:"memory_type"`
	Source     []string `json:"source"`
}

func (c *Client) learn(ctx context.Context, req learnRequest) {
	body, err := json.Marshal(req)
	if err != nil {
		c.logger.Warn("brainlink: marshal learn", slog.String("err", err.Error()))
		return
	}
	url := strings.TrimRight(c.cfg.API, "/") + "/api/brain/learn"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		c.logger.Warn("brainlink: build request", slog.String("err", err.Error()))
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		c.logger.Warn("brainlink: post learn (continuing)", slog.String("err", err.Error()))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.logger.Warn("brainlink: learn rejected", slog.Int("status", resp.StatusCode))
	}
}
