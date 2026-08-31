package brainbox

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// TestLiveRunWorker fires a REAL worker session against the live brainbox API.
// Gated on BRAINBOX_LIVE=1 so it never runs in CI or a normal `make test`.
// Reads BRAINBOX_URL, WORKER_NAME, WORKER_TASK from env. The task must be
// self-contained (clone repo, do work, open PR) — the API carries no repo.
func TestLiveRunWorker(t *testing.T) {
	if os.Getenv("BRAINBOX_LIVE") != "1" {
		t.Skip("set BRAINBOX_LIVE=1 to launch a real worker")
	}
	c := New(os.Getenv("BRAINBOX_URL"), "")
	resp, err := c.RunWorker(context.Background(), WorkerSpec{
		Name:    os.Getenv("WORKER_NAME"),
		Task:    os.Getenv("WORKER_TASK"),
		Backend: "docker",
		Profile: "personal",
	})
	if err != nil {
		t.Fatalf("RunWorker: %v", err)
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	t.Logf("worker launched:\n%s", out)
}
