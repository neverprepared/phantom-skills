// Package nodesync is the transport-agnostic P2P merge engine for phantom-skills
// (mirror of the phantom-brain nodesync). It replicates the immutable
// skill_versions OP-LOG between nodes.
//
// Unlike phantom-brain (per-profile databases), phantom-skills is a single
// database with a `profile` column, so a peer pulls a profile by filtering on
// it (the bearer binding scopes which profile a caller may export). And unlike
// records (no FK), a skill_version references its parent skill by a LOCAL
// skill_id — which differs per node — so the version is exported with its
// parent's logical identity (profile, name) and the receiver re-resolves the
// local skill_id on import. The version NUMBER is likewise local: the receiver
// assigns its own (MAX+1), and dedups by the globally-unique row_ulid and by
// content sha (UNIQUE(skill_id, sha) — identical content authored on two nodes
// converges to one version).
package nodesync

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SkillVersion is the synced projection of a skill_versions row plus the
// logical parent identity needed to re-attach it on the receiver. Local `id`,
// `skill_id`, and `version` are intentionally omitted — they are per-node.
type SkillVersion struct {
	Profile       string          // parent skill identity (with Name)
	Name          string          // parent skill name
	Slug          string          // parent skill slug (for on-demand parent creation)
	SHA           string          // content hash (dedup within a skill)
	Frontmatter   json.RawMessage // jsonb passthrough
	Body          string
	Author        string
	Source        string
	RowULID       string // global dedup identity + cursor
	NodeID        *string
	SyncUpdatedMS int64
}

// ExportSkillVersions returns a profile's skill_versions with row_ulid > since
// in ULID order, so a peer can resume from its cursor. Rows without a row_ulid
// (un-backfilled) are skipped.
func ExportSkillVersions(ctx context.Context, pool *pgxpool.Pool, profile, sinceULID string, limit int) ([]SkillVersion, error) {
	const q = `
		SELECT s.profile, s.name, s.slug,
		       sv.sha, sv.frontmatter, sv.body, sv.author, sv.source,
		       sv.row_ulid, sv.node_id, sv.sync_updated_ms
		FROM skill_versions sv
		JOIN skills s ON s.id = sv.skill_id
		WHERE s.profile = $1
		  AND sv.row_ulid IS NOT NULL
		  AND ($2 = '' OR sv.row_ulid > $2)
		ORDER BY sv.row_ulid ASC
		LIMIT $3`
	rows, err := pool.Query(ctx, q, profile, sinceULID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SkillVersion
	for rows.Next() {
		var v SkillVersion
		if err := rows.Scan(
			&v.Profile, &v.Name, &v.Slug, &v.SHA, &v.Frontmatter, &v.Body,
			&v.Author, &v.Source, &v.RowULID, &v.NodeID, &v.SyncUpdatedMS,
		); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ImportSkillVersions union-merges remote skill_versions locally. Idempotent and
// order-independent. Returns the number of versions newly inserted.
//
// Per row: ensure the parent skill exists (by profile+name); skip if this
// row_ulid is already present (dedup by sync identity) or the same content sha
// already exists for the skill (dedup by content); otherwise insert with a
// locally-assigned version number. The SELECT-then-write is non-atomic, which
// is fine — sync runs single-threaded in one node's merge tick.
func ImportSkillVersions(ctx context.Context, pool *pgxpool.Pool, vers []SkillVersion) (int, error) {
	inserted := 0
	for _, v := range vers {
		if v.RowULID == "" {
			continue // no stable identity → cannot dedup
		}

		// 1. Ensure the parent skill exists (logical identity = profile+name).
		//    The stamp trigger fills the new skill's own sync substrate.
		if _, err := pool.Exec(ctx,
			`INSERT INTO skills (profile, name, slug) VALUES ($1, $2, $3)
			 ON CONFLICT (profile, name) DO NOTHING`,
			v.Profile, v.Name, nonEmpty(v.Slug, v.Name),
		); err != nil {
			return inserted, err
		}

		var skillID int64
		if err := pool.QueryRow(ctx,
			`SELECT id FROM skills WHERE profile = $1 AND name = $2`,
			v.Profile, v.Name,
		).Scan(&skillID); err != nil {
			return inserted, err
		}

		// 2. Dedup: already have this exact version (by sync id) or content?
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(
			     SELECT 1 FROM skill_versions
			     WHERE row_ulid = $1 OR (skill_id = $2 AND sha = $3))`,
			v.RowULID, skillID, v.SHA,
		).Scan(&exists); err != nil {
			return inserted, err
		}
		if exists {
			continue
		}

		// 3. Insert with a locally-assigned, monotonic version number.
		frontmatter := v.Frontmatter
		if len(frontmatter) == 0 {
			frontmatter = json.RawMessage(`{}`)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO skill_versions
			     (skill_id, version, sha, frontmatter, body, author, source,
			      row_ulid, node_id, sync_updated_ms)
			 SELECT $1, COALESCE(MAX(version), 0) + 1, $2, $3, $4, $5, $6, $7, $8, $9
			 FROM skill_versions WHERE skill_id = $1
			 ON CONFLICT (row_ulid) WHERE row_ulid IS NOT NULL DO NOTHING`,
			skillID, v.SHA, frontmatter, v.Body, v.Author, v.Source,
			v.RowULID, v.NodeID, v.SyncUpdatedMS,
		); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
