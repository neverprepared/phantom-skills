package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func skillGetTool() mcp.Tool {
	return mcp.NewTool("skill_get",
		mcp.WithDescription(
			`Fetch a single skill's full current SKILL.md (frontmatter + body) from the shared `+
				`registry by name. Use to inspect a skill's contents before extending or reusing it.`),
		mcp.WithString("name", mcp.Required(), mcp.Description("The skill's name (its directory name).")),
	)
}

func (s *Server) handleSkillGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return mcp.NewToolResultError("name must be non-empty"), nil
	}
	resp, err := s.deps.Client.GetSkill(ctx, name)
	if err != nil {
		return mcp.NewToolResultError("skill_get failed: " + err.Error()), nil
	}
	if resp.Skill == nil {
		return mcp.NewToolResultError("no such skill: " + name), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s  [%s v%d]\n\n", resp.Skill.Name, resp.Skill.Status, resp.Skill.Version)
	if resp.Version != nil {
		if desc, ok := resp.Version.Frontmatter["description"].(string); ok && desc != "" {
			fmt.Fprintf(&b, "description: %s\n\n", desc)
		}
		b.WriteString(resp.Version.Body)
	} else {
		b.WriteString("(skill has no current version yet)")
	}
	return mcp.NewToolResultText(b.String()), nil
}
