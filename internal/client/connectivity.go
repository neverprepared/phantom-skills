package client

import (
	"sync"
	"time"
)

// Connectivity tracks whether the daemon has been reachable recently, so the
// MCP write tools can render a "queued (daemon unreachable since …)" notice
// without every call re-probing.
type Connectivity struct {
	mu          sync.RWMutex
	lastSuccess time.Time
	lastFailure time.Time
	lastErr     string
}

// Snapshot is an immutable view of connectivity state.
type Snapshot struct {
	LastSuccessAt time.Time
	LastFailureAt time.Time
	LastError     string
	// Online is true if the most recent event was a success (or nothing has
	// failed yet).
	Online bool
}

// NoteSuccess records a successful daemon interaction.
func (c *Connectivity) NoteSuccess(t time.Time) {
	c.mu.Lock()
	c.lastSuccess = t
	c.lastErr = ""
	c.mu.Unlock()
}

// NoteFailure records a failed daemon interaction.
func (c *Connectivity) NoteFailure(t time.Time, err error) {
	c.mu.Lock()
	c.lastFailure = t
	if err != nil {
		c.lastErr = err.Error()
	}
	c.mu.Unlock()
}

// Snapshot returns the current state.
func (c *Connectivity) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Snapshot{
		LastSuccessAt: c.lastSuccess,
		LastFailureAt: c.lastFailure,
		LastError:     c.lastErr,
		Online:        !c.lastFailure.After(c.lastSuccess),
	}
}
