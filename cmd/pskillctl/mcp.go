package main

import "github.com/spf13/cobra"

// mcpCmd runs the stdio JSON-RPC MCP server, spawned by Claude Code via its
// mcpServers config. It reads the agent contract from CL_SKILLS_* env vars,
// serves the skill_* tools, and proxies reads/writes to the daemon over HTTP.
//
// Stub until M2 (agent MCP + client + wqueue).
func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run as a stdio JSON-RPC MCP server (spawned by Claude Code)",
		Long: `Starts an MCP server on stdio. Intended to be spawned by Claude Code via
.claude.json mcpServers entries.

Agent contract (env):
  CL_SKILLS_API          daemon URL
  CL_SKILLS_API_TOKEN    bearer token
  CL_WORKSPACE_PROFILE   profile name
  CL_SKILLS_SET          skillset name
  CL_SKILLS_DIR          optional — skills dir (default ~/.claude/skills)`,
		RunE: stub("M2"),
	}
}
