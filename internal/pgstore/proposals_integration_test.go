package pgstore

import (
	"context"
	"testing"
	"time"
)

func TestUsageIngestDedup(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	ts := time.Now().UTC().Truncate(time.Microsecond)
	events := []UsageEvent{
		{SkillName: "s1", SessionID: "sess", Event: "invoked", TS: ts},
		{SkillName: "s1", SessionID: "sess", Event: "invoked", TS: ts}, // exact dup
		{SkillName: "s1", SessionID: "sess", Event: "helpful", TS: ts},
	}
	accepted, err := st.IngestUsage(ctx, "personal", events)
	if err != nil {
		t.Fatal(err)
	}
	if accepted != 2 {
		t.Fatalf("accepted=%d want 2 (one deduped)", accepted)
	}
	// Re-ingesting the same batch accepts nothing new.
	accepted, err = st.IngestUsage(ctx, "personal", events)
	if err != nil {
		t.Fatal(err)
	}
	if accepted != 0 {
		t.Fatalf("re-ingest accepted=%d want 0", accepted)
	}
}

func TestProposalCreateApprove(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id, err := st.CreateProposal(ctx, ProposalInput{
		Profile: "personal", Kind: "create", SkillName: "new-skill",
		ProposedFrontmatter: map[string]any{"name": "new-skill", "description": "d"},
		ProposedBody:        "1. do it", Rationale: "recurred 3x", CreatedBy: "pipeline",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := st.ApproveProposal(ctx, "personal", id, "operator")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if res.Kind != "create" || res.NewStatus != "local" || res.Version != 1 {
		t.Fatalf("apply result: %+v", res)
	}
	sk, _, err := st.GetSkill(ctx, "personal", "new-skill")
	if err != nil {
		t.Fatalf("skill not created: %v", err)
	}
	if sk.Status != "local" {
		t.Fatalf("status=%s want local", sk.Status)
	}

	// Double-approve must fail (no longer pending).
	if _, err := st.ApproveProposal(ctx, "personal", id, "operator"); err != ErrNotFound {
		t.Fatalf("double approve: want ErrNotFound, got %v", err)
	}
}

func TestProposalPromoteThenPrune(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	// Seed a local skill.
	if _, _, err := st.RegisterVersion(ctx, RegisterInput{
		Profile: "personal", Name: "svc", Body: "b", Status: "local",
	}); err != nil {
		t.Fatal(err)
	}

	// Promote it via a proposal.
	pid, _ := st.CreateProposal(ctx, ProposalInput{Profile: "personal", Kind: "promote", SkillName: "svc", CreatedBy: "pipeline"})
	if _, err := st.ApproveProposal(ctx, "personal", pid, "operator"); err != nil {
		t.Fatalf("approve promote: %v", err)
	}
	sk, _, _ := st.GetSkill(ctx, "personal", "svc")
	if sk.Status != "promoted" {
		t.Fatalf("status=%s want promoted", sk.Status)
	}

	// It should now appear in the sync feed as promoted.
	rows, err := st.SyncFeed(ctx, "personal", time.Unix(0, 0), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawPromoted bool
	for _, r := range rows {
		if r.Name == "svc" && r.Status == "promoted" {
			sawPromoted = true
		}
	}
	if !sawPromoted {
		t.Fatal("promoted skill missing from sync feed")
	}

	// Prune it via a proposal.
	rid, _ := st.CreateProposal(ctx, ProposalInput{Profile: "personal", Kind: "prune", SkillName: "svc", CreatedBy: "pipeline"})
	if _, err := st.ApproveProposal(ctx, "personal", rid, "operator"); err != nil {
		t.Fatalf("approve prune: %v", err)
	}
	sk, _, _ = st.GetSkill(ctx, "personal", "svc")
	if sk.Status != "retired" {
		t.Fatalf("status=%s want retired", sk.Status)
	}
}

func TestProposalReject(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id, _ := st.CreateProposal(ctx, ProposalInput{Profile: "personal", Kind: "create", SkillName: "x", CreatedBy: "pipeline"})
	if err := st.RejectProposal(ctx, "personal", id, "operator", "not useful"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	// Approving a rejected proposal must fail.
	if _, err := st.ApproveProposal(ctx, "personal", id, "operator"); err != ErrNotFound {
		t.Fatalf("approve rejected: want ErrNotFound, got %v", err)
	}
	p, err := st.GetProposal(ctx, "personal", id)
	if err != nil || p.Status != "rejected" {
		t.Fatalf("proposal status: %v (%v)", p, err)
	}
}
