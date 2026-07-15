package pgstore

import (
	"context"
	"os"
	"testing"
)

// testStore opens a store against PSKILLS_TEST_DSN, applies migrations, and
// truncates the tables so each test starts clean. Skips when the env var is
// unset so `make test` stays green without a database.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("PSKILLS_TEST_DSN")
	if dsn == "" {
		t.Skip("PSKILLS_TEST_DSN not set; skipping Postgres integration test")
	}
	if err := Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.pool.Exec(context.Background(),
		`TRUNCATE skill_versions, skills RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st
}

func TestRegisterAndGet(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	fm := map[string]any{"name": "demo", "description": "Do a demo. Use when demoing."}

	v1, created, err := st.RegisterVersion(ctx, RegisterInput{
		Profile: "personal", Name: "demo", Frontmatter: fm, Body: "step one", Status: "local",
	})
	if err != nil || !created || v1.Version != 1 {
		t.Fatalf("register v1: v=%v created=%v err=%v", v1, created, err)
	}

	// Idempotent re-register of identical content.
	v1b, created, err := st.RegisterVersion(ctx, RegisterInput{
		Profile: "personal", Name: "demo", Frontmatter: fm, Body: "step one",
	})
	if err != nil || created || v1b.Version != 1 {
		t.Fatalf("idempotent re-register: v=%v created=%v err=%v", v1b, created, err)
	}

	// New content ⇒ new version.
	v2, created, err := st.RegisterVersion(ctx, RegisterInput{
		Profile: "personal", Name: "demo", Frontmatter: fm, Body: "step one and two",
	})
	if err != nil || !created || v2.Version != 2 {
		t.Fatalf("register v2: v=%v created=%v err=%v", v2, created, err)
	}

	sk, cur, err := st.GetSkill(ctx, "personal", "demo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sk.Version != 2 || cur.Body != "step one and two" {
		t.Fatalf("current head wrong: sk.Version=%d body=%q", sk.Version, cur.Body)
	}
	if cur.CreatedAt.IsZero() {
		t.Fatal("version created_at is zero")
	}
	if cur.Frontmatter["description"] != fm["description"] {
		t.Fatalf("frontmatter not round-tripped: %v", cur.Frontmatter)
	}
}

func TestListFilterAndRetire(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	mk := func(name, status string) {
		if _, _, err := st.RegisterVersion(ctx, RegisterInput{
			Profile: "personal", Name: name, Body: "b-" + name, Status: status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("alpha", "local")
	mk("beta", "promoted")
	mk("gamma", "local")

	local, err := st.ListSkills(ctx, "personal", ListOpts{Status: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if len(local) != 2 {
		t.Fatalf("want 2 local skills, got %d", len(local))
	}

	if err := st.RetireSkill(ctx, "personal", "beta"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if err := st.RetireSkill(ctx, "personal", "nope"); err != ErrNotFound {
		t.Fatalf("retire missing: want ErrNotFound, got %v", err)
	}

	_, _, err = st.GetSkill(ctx, "personal", "missing")
	if err != ErrNotFound {
		t.Fatalf("get missing: want ErrNotFound, got %v", err)
	}
}

func TestKeysetPaging(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	for _, n := range []string{"one", "two", "three"} {
		if _, _, err := st.RegisterVersion(ctx, RegisterInput{Profile: "personal", Name: n, Body: n}); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := st.ListSkills(ctx, "personal", ListOpts{Limit: 2})
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1: n=%d err=%v", len(page1), err)
	}
	page2, err := st.ListSkills(ctx, "personal", ListOpts{Limit: 2, AfterID: page1[len(page1)-1].ID})
	if err != nil || len(page2) != 1 {
		t.Fatalf("page2: n=%d err=%v", len(page2), err)
	}
}
