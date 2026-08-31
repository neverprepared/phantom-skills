package nodesync

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/neverprepared/phantom-skills/internal/pgstore"
)

// Gate: set PS_TEST_BASE_DSN to a maintenance DSN, e.g.
//   PS_TEST_BASE_DSN=postgres://postgres:postgres@localhost:5433/postgres
// Two databases stand in for two independently-writing nodes.
func testBaseDSN(t *testing.T) string {
	dsn := os.Getenv("PS_TEST_BASE_DSN")
	if dsn == "" {
		t.Skip("set PS_TEST_BASE_DSN to run nodesync integration tests")
	}
	return dsn
}

func dsnForDB(t *testing.T, base, db string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse base DSN: %v", err)
	}
	u.Path = "/" + db
	return u.String()
}

// createNode creates the database if absent, migrates it, stamps a per-DB node
// id GUC, and returns a pool.
func createNode(t *testing.T, ctx context.Context, base, db, nodeID string) *pgxpool.Pool {
	t.Helper()
	maint, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect maintenance: %v", err)
	}
	var exists bool
	if err := maint.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", db).Scan(&exists); err != nil {
		maint.Close(ctx)
		t.Fatalf("check db existence: %v", err)
	}
	if !exists {
		if _, err := maint.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
			maint.Close(ctx)
			t.Fatalf("create db %s: %v", db, err)
		}
	}
	if _, err := maint.Exec(ctx,
		"ALTER DATABASE "+pgx.Identifier{db}.Sanitize()+" SET ps.node_id = '"+nodeID+"'"); err != nil {
		maint.Close(ctx)
		t.Fatalf("set node_id: %v", err)
	}
	maint.Close(ctx)

	dsn := dsnForDB(t, base, db)
	if err := pgstore.Migrate(dsn); err != nil {
		t.Fatalf("migrate %s: %v", db, err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool %s: %v", db, err)
	}
	// Clean slate for re-runnable tests.
	if _, err := pool.Exec(ctx,
		"TRUNCATE skill_versions, skills RESTART IDENTITY CASCADE"); err != nil {
		pool.Close()
		t.Fatalf("truncate %s: %v", db, err)
	}
	return pool
}

func TestSkillVersionSync(t *testing.T) {
	base := testBaseDSN(t)
	ctx := context.Background()

	a := createNode(t, ctx, base, "skills_p2p_a", "node-a")
	defer a.Close()
	b := createNode(t, ctx, base, "skills_p2p_b", "node-b")
	defer b.Close()

	// Author a skill + one version on node A. Triggers stamp the substrate.
	var skillID int64
	if err := a.QueryRow(ctx,
		`INSERT INTO skills (profile, name, slug) VALUES ('personal','demo','demo') RETURNING id`).
		Scan(&skillID); err != nil {
		t.Fatalf("insert skill on A: %v", err)
	}
	if _, err := a.Exec(ctx,
		`INSERT INTO skill_versions (skill_id, version, sha, body, author, source)
		 VALUES ($1, 1, 'sha-abc', 'step one', 'node-a', 'test')`, skillID); err != nil {
		t.Fatalf("insert version on A: %v", err)
	}
	var srcULID, srcNode string
	if err := a.QueryRow(ctx,
		`SELECT row_ulid, node_id FROM skill_versions WHERE sha='sha-abc'`).Scan(&srcULID, &srcNode); err != nil {
		t.Fatalf("read stamped version: %v", err)
	}
	if len(srcULID) != 26 {
		t.Fatalf("trigger did not stamp a 26-char row_ulid: %q", srcULID)
	}
	if srcNode != "node-a" {
		t.Fatalf("node_id = %q, want node-a", srcNode)
	}

	// Export from A → import into B → B converges (parent skill re-created,
	// version attached under B's local skill_id).
	vers, err := ExportSkillVersions(ctx, a, "personal", "", 500)
	if err != nil {
		t.Fatalf("export A: %v", err)
	}
	if len(vers) != 1 {
		t.Fatalf("exported %d versions, want 1", len(vers))
	}
	n, err := ImportSkillVersions(ctx, b, vers)
	if err != nil {
		t.Fatalf("import to B: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported %d, want 1", n)
	}

	// Assert B has the skill and the version, with the origin row_ulid preserved.
	var bBody, bULID, bParentName string
	if err := b.QueryRow(ctx,
		`SELECT sv.body, sv.row_ulid, s.name
		 FROM skill_versions sv JOIN skills s ON s.id = sv.skill_id
		 WHERE sv.sha='sha-abc'`).Scan(&bBody, &bULID, &bParentName); err != nil {
		t.Fatalf("read B version: %v", err)
	}
	if bBody != "step one" || bParentName != "demo" {
		t.Fatalf("B version mismatch: body=%q parent=%q", bBody, bParentName)
	}
	if bULID != srcULID {
		t.Fatalf("row_ulid not preserved across nodes: A=%q B=%q", srcULID, bULID)
	}

	// Idempotency: re-import the same batch → no new versions, no duplicate.
	n2, err := ImportSkillVersions(ctx, b, vers)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("re-import inserted %d, want 0 (idempotent)", n2)
	}
	var count int
	if err := b.QueryRow(ctx, `SELECT count(*) FROM skill_versions`).Scan(&count); err != nil {
		t.Fatalf("count B versions: %v", err)
	}
	if count != 1 {
		t.Fatalf("B has %d versions after re-import, want 1", count)
	}

	// Content dedup: identical content (same sha) with a DIFFERENT row_ulid must
	// not create a second version on B (UNIQUE(skill_id, sha) convergence).
	dup := vers[0]
	dup.RowULID = strings.Repeat("Z", 26) // a different, valid-length ULID
	n3, err := ImportSkillVersions(ctx, b, []SkillVersion{dup})
	if err != nil {
		t.Fatalf("import dup content: %v", err)
	}
	if n3 != 0 {
		t.Fatalf("dup-content import inserted %d, want 0 (content dedup)", n3)
	}
}
