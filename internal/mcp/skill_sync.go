package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/neverprepared/phantom-skills/internal/syncer"
)

func skillSyncTool() mcp.Tool {
	return mcp.NewTool("skill_sync",
		mcp.WithDescription(
			`Pull the latest shared skills from the registry into this machine's skills dir `+
				`(~/.claude/skills), so newly-promoted skills become available. Only touches `+
				`phantom-managed skill dirs; hand-authored skills are left untouched. Run when you `+
				`want the freshest shared skills mid-session.`),
	)
}

func (s *Server) handleSkillSync(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.Syncer == nil {
		return mcp.NewToolResultError("skill_sync is not available (no syncer configured)"), nil
	}
	res, err := s.deps.Syncer.Sync(ctx, syncer.Options{})
	if err != nil {
		return mcp.NewToolResultError("skill_sync failed: " + err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf(
		"Synced skills into %s\nwritten=%d up-to-date=%d deleted=%d skipped-unmanaged=%d",
		s.deps.Agent.SkillsDir, res.Written, res.UpToDate, res.Deleted, res.SkippedUnmanaged)), nil
}
