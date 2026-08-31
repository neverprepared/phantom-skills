-- Down: remove the additive P2P sync substrate.
DROP TRIGGER IF EXISTS skill_versions_stamp_sync ON skill_versions;
DROP TRIGGER IF EXISTS skills_stamp_sync ON skills;
DROP FUNCTION IF EXISTS ps_stamp_sync();
DROP FUNCTION IF EXISTS ps_gen_ulid();

DROP INDEX IF EXISTS idx_skill_versions_row_ulid_ord;
DROP INDEX IF EXISTS uq_skill_versions_row_ulid;
DROP INDEX IF EXISTS uq_skills_row_ulid;

ALTER TABLE skill_versions DROP COLUMN IF EXISTS sync_updated_ms;
ALTER TABLE skill_versions DROP COLUMN IF EXISTS node_id;
ALTER TABLE skill_versions DROP COLUMN IF EXISTS row_ulid;

ALTER TABLE skills DROP COLUMN IF EXISTS sync_updated_ms;
ALTER TABLE skills DROP COLUMN IF EXISTS node_id;
ALTER TABLE skills DROP COLUMN IF EXISTS row_ulid;
