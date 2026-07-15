package wqueue

import (
	"context"
	"errors"
	"testing"
	"time"
)

func open(t *testing.T) *Queue {
	t.Helper()
	q, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return q
}

func TestEnqueueDepthDelete(t *testing.T) {
	q := open(t)
	ctx := context.Background()
	if _, err := q.Enqueue(ctx, KindUsage, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	it, err := q.Enqueue(ctx, KindProposal, []byte(`{"b":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := q.Depth(ctx); n != 2 {
		t.Fatalf("depth=%d want 2", n)
	}
	if err := q.Delete(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.Depth(ctx); n != 1 {
		t.Fatalf("depth=%d want 1", n)
	}
}

func TestInvalidKindRejected(t *testing.T) {
	q := open(t)
	if _, err := q.Enqueue(context.Background(), Kind("bogus"), []byte(`{}`)); err == nil {
		t.Fatal("expected invalid-kind error")
	}
}

func TestEligibilityRespectsBackoff(t *testing.T) {
	q := open(t)
	ctx := context.Background()
	it, _ := q.Enqueue(ctx, KindUsage, []byte(`{}`))

	now := time.Now()
	eligible, _ := q.NextEligible(ctx, now, 10)
	if len(eligible) != 1 {
		t.Fatalf("fresh item should be eligible, got %d", len(eligible))
	}

	// A failed attempt schedules the next try in the future ⇒ not eligible now.
	if err := q.MarkAttempt(ctx, it.ID, now, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	eligible, _ = q.NextEligible(ctx, now, 10)
	if len(eligible) != 0 {
		t.Fatalf("backed-off item should not be eligible now, got %d", len(eligible))
	}
	// But it is eligible once its backoff has elapsed.
	eligible, _ = q.NextEligible(ctx, now.Add(BackoffFor(1)+time.Second), 10)
	if len(eligible) != 1 {
		t.Fatalf("item should be eligible after backoff, got %d", len(eligible))
	}
}

func TestDeadLettering(t *testing.T) {
	q := open(t)
	ctx := context.Background()
	it, _ := q.Enqueue(ctx, KindUsage, []byte(`{}`))
	for i := 0; i < maxAttempts; i++ {
		if err := q.MarkAttempt(ctx, it.ID, time.Now(), errors.New("nope")); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := q.Depth(ctx); n != 0 {
		t.Fatalf("dead item should not count toward depth, got %d", n)
	}
	live, _ := q.List(ctx, false, 10)
	if len(live) != 0 {
		t.Fatalf("dead item should be excluded from live list, got %d", len(live))
	}
	all, _ := q.List(ctx, true, 10)
	if len(all) != 1 || !all[0].Dead {
		t.Fatalf("dead item should appear in all-list as dead: %+v", all)
	}

	// Purge --dead removes it.
	n, _ := q.Purge(ctx, true, false)
	if n != 1 {
		t.Fatalf("purge dead removed %d want 1", n)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	if BackoffFor(1) != 2*time.Second {
		t.Fatalf("BackoffFor(1)=%v", BackoffFor(1))
	}
	if BackoffFor(3) != 8*time.Second {
		t.Fatalf("BackoffFor(3)=%v", BackoffFor(3))
	}
	if BackoffFor(100) != 30*time.Minute {
		t.Fatalf("BackoffFor(100) should cap at 30m, got %v", BackoffFor(100))
	}
}
