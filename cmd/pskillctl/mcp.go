package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
	"github.com/neverprepared/phantom-skills/internal/config"
	pkmcp "github.com/neverprepared/phantom-skills/internal/mcp"
	"github.com/neverprepared/phantom-skills/internal/syncer"
	"github.com/neverprepared/phantom-skills/internal/version"
)

// mcpCmd runs the stdio JSON-RPC MCP server, spawned by Claude Code. It reads
// the agent contract from CL_SKILLS_* env, exposes the skill_* tools, proxies
// reads/writes to the daemon over HTTP, and drains queued writes in the
// background.
func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run as a stdio JSON-RPC MCP server (spawned by Claude Code)",
		Long: `Starts an MCP server on stdio. Intended to be spawned by Claude Code via
.claude.json mcpServers entries.

Agent contract (env):
  CL_SKILLS_API          daemon URL              (required)
  CL_SKILLS_API_TOKEN    bearer token            (required)
  CL_WORKSPACE_PROFILE   profile name            (required)
  CL_SKILLS_SET          skillset name           (default "default")
  CL_SKILLS_DIR          skills dir              (default ~/.claude/skills)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCP()
		},
	}
}

func runMCP() error {
	agent, err := config.LoadAgent()
	if err != nil {
		return err
	}

	// MCP uses stdout for JSON-RPC; the logger MUST go to stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	c, err := client.New(client.Opts{BaseURL: agent.API, Token: agent.Token})
	if err != nil {
		return err
	}
	q, err := wqueue.Open(filepath.Join(agent.StateDir(), "queue"))
	if err != nil {
		return err
	}
	defer q.Close()
	conn := &client.Connectivity{}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Background drainer flushes queued writes as the daemon becomes reachable.
	go client.NewDrainer(q, c, conn, 30*time.Second, logger).Run(ctx)

	sy := syncer.New(c, agent.SkillsDir, agent.StateDir(), logger)

	srv := server.NewMCPServer("phantom-skills", version.Version, server.WithToolCapabilities(false))
	pkmcp.NewServer(pkmcp.ServerDeps{
		Client: c,
		Queue:  q,
		Conn:   conn,
		Syncer: sy,
		Agent:  *agent,
	}).Register(srv)

	srvErr := make(chan error, 1)
	go func() { srvErr <- server.ServeStdio(srv) }()
	select {
	case <-ctx.Done():
		logger.Info("phantom-skills: shutdown signal received")
		return nil
	case err := <-srvErr:
		return err
	}
}
