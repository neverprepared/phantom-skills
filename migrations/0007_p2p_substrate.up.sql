-- 0007_p2p_substrate — additive P2P sync substrate for skills + skill_versions.
--
-- Mirrors the phantom-brain records substrate (migration 0009 there) so the two
-- Go services share one sync pattern. Additive only: new columns are
-- nullable/defaulted, no read-path or delete-semantics change. Local `id`
-- IDENTITY PKs stay the local order and are never synced.
--
-- skill_versions is an OP-LOG (immutable, content-addressed) → merged by UNION
-- on the globally-unique row_ulid. skills is the logical head, keyed across
-- nodes by its UNIQUE(profile, name) — a version arriving from a peer resolves
-- its parent by (profile, name), never by the peer's local skill_id.
--
--   * row_ulid        — globally-unique, time-sortable sync identity + cursor.
--   * node_id         — origin node (provenance).
--   * sync_updated_ms — trigger-immune LWW clock (NOT the updated_at that
--                       set_updated_at() owns, so a merged row's clock survives).

ALTER TABLE skills         ADD COLUMN IF NOT EXISTS row_ulid        text;
ALTER TABLE skills         ADD COLUMN IF NOT EXISTS node_id         text;
ALTER TABLE skills         ADD COLUMN IF NOT EXISTS sync_updated_ms bigint;

ALTER TABLE skill_versions ADD COLUMN IF NOT EXISTS row_ulid        text;
ALTER TABLE skill_versions ADD COLUMN IF NOT EXISTS node_id         text;
ALTER TABLE skill_versions ADD COLUMN IF NOT EXISTS sync_updated_ms bigint;

CREATE UNIQUE INDEX IF NOT EXISTS uq_skills_row_ulid
    ON skills (row_ulid) WHERE row_ulid IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_skill_versions_row_ulid
    ON skill_versions (row_ulid) WHERE row_ulid IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_skill_versions_row_ulid_ord
    ON skill_versions (row_ulid) WHERE row_ulid IS NOT NULL;

-- ULID generator: 48-bit ms time (10 Crockford chars, MSB-first) + 80-bit
-- randomness (16 chars). Time-sortable; interoperable with the Go nodeid port.
CREATE OR REPLACE FUNCTION ps_gen_ulid() RETURNS text AS $$
DECLARE
    alphabet text := '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
    ts bigint := floor(extract(epoch from clock_timestamp()) * 1000)::bigint;
    out text := '';
    i int;
    v bigint;
BEGIN
    v := ts;
    FOR i IN 1..10 LOOP
        out := substr(alphabet, (v & 31)::int + 1, 1) || out;
        v := v >> 5;
    END LOOP;
    FOR i IN 1..16 LOOP
        out := out || substr(alphabet, floor(random() * 32)::int + 1, 1);
    END LOOP;
    RETURN out;
END;
$$ LANGUAGE plpgsql;

-- Stamp only NULL columns: a local write is stamped; a merge INSERT arrives with
-- the origin values populated and is left intact. node_id from a per-DB GUC.
CREATE OR REPLACE FUNCTION ps_stamp_sync() RETURNS trigger AS $$
BEGIN
    IF NEW.row_ulid IS NULL THEN
        NEW.row_ulid := ps_gen_ulid();
    END IF;
    IF NEW.node_id IS NULL THEN
        NEW.node_id := current_setting('ps.node_id', true);
    END IF;
    IF NEW.sync_updated_ms IS NULL THEN
        NEW.sync_updated_ms := floor(extract(epoch from clock_timestamp()) * 1000)::bigint;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS skills_stamp_sync ON skills;
CREATE TRIGGER skills_stamp_sync
    BEFORE INSERT ON skills
    FOR EACH ROW EXECUTE FUNCTION ps_stamp_sync();

DROP TRIGGER IF EXISTS skill_versions_stamp_sync ON skill_versions;
CREATE TRIGGER skill_versions_stamp_sync
    BEFORE INSERT ON skill_versions
    FOR EACH ROW EXECUTE FUNCTION ps_stamp_sync();
