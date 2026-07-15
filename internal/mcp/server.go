// Package mcp wires the pskillctl agent-side MCP server: it registers the
// skill_* tool handlers against an injected dependency set (daemon client,
// write-ahead queue, connectivity tracker, skills dir). Tools live in sibling
// files so each is self-contained; Server is the thin glue.
//
// Reads (skill_list, skill_get) are online-only and return a clear error when
// the daemon is unreachable. Writes (skill_usage_report, skill_propose) go
// through enqueueAndAttempt so an offline session degrades to "queued" instead
// of failing.
package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
	"github.com/neverprepared/phantom-skills/internal/config"
)

// ServerDeps is the dependency container the tool handlers close over.
type ServerDeps struct {
	// Client is the daemon HTTP client. Reads go straight to it; writes are
	// attempted through it after being persisted to Queue.
	Client *client.Client

	// Queue is the offline write-ahead queue. Writes are enqueued first, then
	// attempted; on daemon failure the row survives for the drainer.
	Queue *wqueue.Queue

	// Conn tracks daemon reachability so write tools can render a queued-notice.
	Conn *client.Connectivity

	// Agent is this session's resolved contract (profile, skillset, skills dir).
	Agent config.Agent
}

// Server is the MCP tool registry. Construct once per process, hand to
// Register, then ServeStdio.
type Server struct {
	deps ServerDeps
}

// NewServer wraps a ServerDeps so its tools can be registered.
func NewServer(deps ServerDeps) *Server { return &Server{deps: deps} }

// Register attaches every agent-side skill_* tool to the mcp-go server.
func (s *Server) Register(srv *server.MCPServer) {
	srv.AddTool(skillListTool(), s.handleSkillList)
	srv.AddTool(skillGetTool(), s.handleSkillGet)
	srv.AddTool(skillUsageReportTool(), s.handleSkillUsageReport)
	srv.AddTool(skillProposeTool(), s.handleSkillPropose)
}
