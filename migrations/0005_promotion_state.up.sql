-- 0005_promotion_state — local-vs-promoted bookkeeping.
--
-- One row per skill that has ever been promoted. Records when/by whom it was
-- promoted (or later demoted). sync_generation is reserved for cross-machine
-- promotion accounting in M10.

CREATE TABLE promotion_state (
    skill_id        bigint PRIMARY KEY REFERENCES skills(id) ON DELETE CASCADE,
    promoted_at     timestamptz,
    promoted_by     text,
    demoted_at      timestamptz,
    sync_generation bigint NOT NULL DEFAULT 0
);
