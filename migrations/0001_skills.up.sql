-- 0001_skills — the logical skill identity (System of Record).
--
-- One row per logical skill (a "name" within a profile). The mutable status
-- and a pointer to the current immutable version live here; the version
-- content lives in skill_versions (0002). current_version_id has no FK yet —
-- the referenced table doesn't exist until 0002, which adds the constraint.

-- Shared trigger: bump updated_at on any row update.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE skills (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile            text NOT NULL,
    name               text NOT NULL,
    slug               text NOT NULL,
    status             text NOT NULL DEFAULT 'draft',   -- draft|local|promoted|retired
    origin             text NOT NULL DEFAULT 'authored', -- authored|proposed|imported
    tags               text[] NOT NULL DEFAULT '{}',
    current_version_id bigint,                            -- FK added in 0002
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT skills_status_chk CHECK (status IN ('draft','local','promoted','retired')),
    CONSTRAINT skills_origin_chk CHECK (origin IN ('authored','proposed','imported')),
    CONSTRAINT skills_uniq UNIQUE (profile, name)
);

CREATE TRIGGER skills_set_updated_at
    BEFORE UPDATE ON skills
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX skills_profile_idx ON skills (profile);
CREATE INDEX skills_status_idx  ON skills (profile, status);
CREATE INDEX skills_tags_gin    ON skills USING gin (tags);
