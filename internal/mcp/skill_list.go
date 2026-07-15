package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/neverprepared/phantom-skills/internal/client"
)

func skillListTool() mcp.Tool {
	return mcp.NewTool("skill_list",
		mcp.WithDescription(
			`List the skills available in the shared registry for this scope, with their `+
				`status and one-line description. Use to see what reusable skills already exist `+
				`before proposing a new one.`),
		mcp.WithString("status", mcp.Description("Filter by status: draft|local|promoted|retired.")),
		mcp.WithString("tag", mcp.Description("Filter to skills carrying this tag.")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 100).")),
	)
}

func (s *Server) handleSkillList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status, _ := req.RequireString("status")
	tag, _ := req.RequireString("tag")
	limit := 0
	if got, err := req.RequireFloat("limit"); err == nil {
		limit = int(got)
	}
	resp, err := s.deps.Client.ListSkills(ctx, client.ListOpts{Status: status, Tag: tag, Limit: limit})
	if err != nil {
		return mcp.NewToolResultError("skill_list failed: " + err.Error()), nil
	}
	if len(resp.Skills) == 0 {
		return mcp.NewToolResultText("No skills in the registry for this scope."), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s):\n", len(resp.Skills))
	for _, sk := range resp.Skills {
		fmt.Fprintf(&b, "- %s  [%s v%d]\n", sk.Name, sk.Status, sk.Version)
		if len(sk.Tags) > 0 {
			fmt.Fprintf(&b, "    tags: %s\n", strings.Join(sk.Tags, ", "))
		}
	}
	return mcp.NewToolResultText(b.String()), nil
}
