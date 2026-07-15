package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
)

func skillProposeTool() mcp.Tool {
	return mcp.NewTool("skill_propose",
		mcp.WithDescription(
			`Propose a new skill (or an edit) you discovered was worth capturing — a reusable `+
				`procedure you'd want available next time. The daemon queues it as a create `+
				`proposal for review (human-gated by default). Provide a sharp description: it is `+
				`the trigger the model matches against, so state WHAT it does AND WHEN to use it.`),
		mcp.WithString("name", mcp.Required(), mcp.Description("Proposed skill name (lowercase-hyphen).")),
		mcp.WithString("description", mcp.Required(), mcp.Description("The trigger: what it does AND when to use it (<1024 chars).")),
		mcp.WithString("body", mcp.Required(), mcp.Description("The SKILL.md body: generalized steps to perform the procedure.")),
		mcp.WithString("rationale", mcp.Description("Why this is worth capturing (evidence, recurrence).")),
		mcp.WithString("session_id", mcp.Description("Optional session identifier.")),
	)
}

func (s *Server) handleSkillPropose(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	description, err := req.RequireString("description")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(body) == "" {
		return mcp.NewToolResultError("name and body must be non-empty"), nil
	}
	rationale, _ := req.RequireString("rationale")
	sessionID, _ := req.RequireString("session_id")

	payload, err := json.Marshal(map[string]any{
		"kind":       "create",
		"skill_name": name,
		"proposed_frontmatter": map[string]any{
			"name":        name,
			"description": strings.TrimSpace(description),
		},
		"proposed_body": body,
		"rationale":     rationale,
		"session_id":    sessionID,
	})
	if err != nil {
		return mcp.NewToolResultError("encode proposal: " + err.Error()), nil
	}

	res, err := s.enqueueAndAttempt(ctx, wqueue.KindProposal, payload, func(ctx context.Context) error {
		_, perr := s.deps.Client.PostProposal(ctx, payload)
		return perr
	})
	if err != nil {
		return mcp.NewToolResultError("skill_propose failed: " + err.Error()), nil
	}
	return mcp.NewToolResultText("Submitted create proposal for " + name + " (human-gated review)." + res.Notice), nil
}
