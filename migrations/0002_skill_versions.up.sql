-- 0002_skill_versions — immutable, content-addressed versions of a skill.
--
-- Each row is one frozen SKILL.md (frontmatter + body) plus provenance. A new
-- version is appended whenever content changes; re-registering identical
-- content is a no-op (UNIQUE(skill_id, sha) — dedup per skill, not globally,
-- so two different skills may legitimately share a body). skills.current_version_id
-- points at the head; this migration wires that FK now that the table exists.

CREATE TABLE skill_versions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    skill_id     bigint NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version      int NOT NULL,
    sha          text NOT NULL,                      -- sha256 of canonical frontmatter + body
    frontmatter  jsonb NOT NULL DEFAULT '{}',
    body         text NOT NULL DEFAULT '',
    author       text NOT NULL DEFAULT '',           -- who/what produced it
    source       text NOT NULL DEFAULT '',           -- session id, operator, pipeline
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT skill_versions_uniq_ver UNIQUE (skill_id, version),
    CONSTRAINT skill_versions_uniq_sha UNIQUE (skill_id, sha)
);

CREATE INDEX skill_versions_skill_idx ON skill_versions (skill_id);
CREATE INDEX skill_versions_sha_idx   ON skill_versions (sha);

ALTER TABLE skills
    ADD CONSTRAINT skills_current_version_fk
    FOREIGN KEY (current_version_id) REFERENCES skill_versions(id) ON DELETE SET NULL;
