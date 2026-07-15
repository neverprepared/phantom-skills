package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
)

var validUsageEvents = map[string]bool{"invoked": true, "helpful": true, "ignored": true, "error": true}

func skillUsageReportTool() mcp.Tool {
	return mcp.NewTool("skill_usage_report",
		mcp.WithDescription(
			`Record a usage event for a skill so the daemon can measure which skills help. `+
				`Call after using (or deciding not to use) a skill. Events: invoked (loaded it), `+
				`helpful (it led to success), ignored (relevant but not used), error (it misled). `+
				`Writes are queued and sync when the daemon is reachable.`),
		mcp.WithString("skill", mcp.Required(), mcp.Description("The skill's name.")),
		mcp.WithString("event", mcp.Required(), mcp.Description("invoked | helpful | ignored | error")),
		mcp.WithString("session_id", mcp.Description("Optional session identifier.")),
		mcp.WithString("note", mcp.Description("Optional free-text context.")),
	)
}

func (s *Server) handleSkillUsageReport(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	skill, err := req.RequireString("skill")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	event, err := req.RequireString("event")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	event = strings.TrimSpace(event)
	if !validUsageEvents[event] {
		return mcp.NewToolResultError("event must be one of: invoked, helpful, ignored, error"), nil
	}
	sessionID, _ := req.RequireString("session_id")
	note, _ := req.RequireString("note")
	host, _ := os.Hostname()

	ev := map[string]any{
		"skill":   strings.TrimSpace(skill),
		"event":   event,
		"session": sessionID,
		"machine": host,
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if note != "" {
		ev["context"] = map[string]any{"note": note}
	}
	payload, err := json.Marshal(map[string]any{"events": []any{ev}})
	if err != nil {
		return mcp.NewToolResultError("encode usage: " + err.Error()), nil
	}

	res, err := s.enqueueAndAttempt(ctx, wqueue.KindUsage, payload, func(ctx context.Context) error {
		return s.deps.Client.PostUsage(ctx, payload)
	})
	if err != nil {
		return mcp.NewToolResultError("skill_usage_report failed: " + err.Error()), nil
	}
	return mcp.NewToolResultText("Recorded usage event for " + skill + "." + res.Notice), nil
}
