package pgstore

import (
	"context"
	"testing"
	"time"
)

func TestRatchetLedgerDedupAndRateLimit(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	// No ratchets yet.
	if inflight, _ := st.HasInFlightRatchet(ctx, "personal", "svc"); inflight {
		t.Fatal("expected no in-flight ratchet")
	}
	if n, _ := st.CountInFlightRatchets(ctx, "personal"); n != 0 {
		t.Fatalf("in-flight count = %d want 0", n)
	}

	// Fire one for svc.
	id, err := st.RecordRatchet(ctx, "personal", RatchetRun{SkillName: "svc", Kind: "edit", TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}

	// Dedup guard now trips for svc.
	if inflight, _ := st.HasInFlightRatchet(ctx, "personal", "svc"); !inflight {
		t.Fatal("svc should be in-flight")
	}
	// A different skill is unaffected.
	if inflight, _ := st.HasInFlightRatchet(ctx, "personal", "other"); inflight {
		t.Fatal("other should not be in-flight")
	}
	// Rate-limit input sees it.
	if n, _ := st.CountInFlightRatchets(ctx, "personal"); n != 1 {
		t.Fatalf("in-flight count = %d want 1", n)
	}
	// Cooldown/anti-nag sees the recent run.
	if recent, _ := st.RecentlyRatcheted(ctx, "personal", "svc", time.Hour); !recent {
		t.Fatal("svc should be recently ratcheted")
	}

	// PR opens → still in-flight (awaiting the merge decision).
	if err := st.UpdateRatchetStatus(ctx, id, "pr_open", "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if inflight, _ := st.HasInFlightRatchet(ctx, "personal", "svc"); !inflight {
		t.Fatal("pr_open still counts as in-flight")
	}

	// Human closes the PR without merging → no longer in-flight, but the
	// cooldown still suppresses an immediate re-fire.
	if err := st.UpdateRatchetStatus(ctx, id, "closed", ""); err != nil {
		t.Fatal(err)
	}
	if inflight, _ := st.HasInFlightRatchet(ctx, "personal", "svc"); inflight {
		t.Fatal("closed run must not count as in-flight")
	}
	if recent, _ := st.RecentlyRatcheted(ctx, "personal", "svc", time.Hour); !recent {
		t.Fatal("closed run is still within cooldown")
	}
	// But a zero-width cooldown lets it through.
	if recent, _ := st.RecentlyRatcheted(ctx, "personal", "svc", time.Nanosecond); recent {
		t.Fatal("nanosecond cooldown should not suppress")
	}

	list, err := st.ListRatchets(ctx, "personal", "")
	if err != nil || len(list) != 1 || list[0].PRURL == "" {
		t.Fatalf("list ratchets: %v (%v)", list, err)
	}
}
