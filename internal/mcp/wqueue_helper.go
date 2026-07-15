package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/neverprepared/phantom-skills/internal/client"
	"github.com/neverprepared/phantom-skills/internal/client/wqueue"
)

// daemonOp performs the actual daemon write for an enqueued item. Each write
// handler supplies a closure calling the right client method with the payload
// it already has in hand.
type daemonOp func(ctx context.Context) error

// enqueueResult reports whether a write reached the daemon and, if not, a
// human-readable notice to append to the tool result.
type enqueueResult struct {
	Posted bool
	Notice string
}

// enqueueAndAttempt persists the write to the wqueue, immediately tries op, and
// returns a notice ("" on success). On daemon failure the row stays queued, the
// connectivity state flips offline, and the caller surfaces success-with-notice
// — never an error. An error is returned ONLY for queue I/O failures (real
// problems the caller should surface).
func (s *Server) enqueueAndAttempt(ctx context.Context, kind wqueue.Kind, payload []byte, op daemonOp) (enqueueResult, error) {
	if s.deps.Queue == nil {
		// No queue wired (e.g. a test with a direct client) — attempt directly.
		if err := op(ctx); err != nil {
			return enqueueResult{}, err
		}
		return enqueueResult{Posted: true}, nil
	}
	item, err := s.deps.Queue.Enqueue(ctx, kind, payload)
	if err != nil {
		return enqueueResult{}, fmt.Errorf("enqueue: %w", err)
	}
	now := time.Now()
	if opErr := op(ctx); opErr != nil {
		_ = s.deps.Queue.MarkAttempt(ctx, item.ID, now, opErr)
		if s.deps.Conn != nil {
			s.deps.Conn.NoteFailure(now, opErr)
		}
		depth, _ := s.deps.Queue.Depth(ctx)
		return enqueueResult{Posted: false, Notice: formatQueueNotice(s.connSnapshot(), depth)}, nil
	}
	_ = s.deps.Queue.Delete(ctx, item.ID)
	if s.deps.Conn != nil {
		s.deps.Conn.NoteSuccess(now)
	}
	return enqueueResult{Posted: true}, nil
}

func (s *Server) connSnapshot() client.Snapshot {
	if s.deps.Conn == nil {
		return client.Snapshot{}
	}
	return s.deps.Conn.Snapshot()
}

// formatQueueNotice renders the user-facing "your write is queued" line: one
// sentence conveying both outage age and pending depth.
func formatQueueNotice(snap client.Snapshot, depth int) string {
	var since string
	if snap.LastSuccessAt.IsZero() {
		since = "since process start"
	} else {
		since = humanizeAge(time.Since(snap.LastSuccessAt)) + " ago"
	}
	plural := "writes"
	if depth == 1 {
		plural = "write"
	}
	return fmt.Sprintf("\n\nQueued (daemon unreachable %s). %d %s pending sync.", since, depth, plural)
}

// humanizeAge renders a Duration as a terse "10s"/"2m"/"1h"/"3d".
func humanizeAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
