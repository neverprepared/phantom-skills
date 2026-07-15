-- 0006_ratchet_runs — the auto-fire ledger.
--
-- One row per ratchet the daemon fires at brainbox. This is the dedup +
-- rate-limit backbone for Option B's auto-fire: never launch a second ratchet
-- for a skill that already has one in flight, respect a per-skill cooldown
-- after a run, and cap total concurrent ratchets. It also tracks each run's
-- resulting PR so the daemon can reconcile status.

CREATE TABLE ratchet_runs (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile    text NOT NULL,
    skill_name text NOT NULL,
    kind       text NOT NULL,                    -- create | edit | prune | merge
    task_id    text NOT NULL DEFAULT '',         -- brainbox hub task id
    job_id     text NOT NULL DEFAULT '',
    branch     text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT 'fired',    -- fired | pr_open | merged | closed | failed
    pr_url     text NOT NULL DEFAULT '',
    rationale  text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT ratchet_kind_chk   CHECK (kind IN ('create','edit','prune','merge')),
    CONSTRAINT ratchet_status_chk CHECK (status IN ('fired','pr_open','merged','closed','failed'))
);

CREATE INDEX ratchet_skill_idx  ON ratchet_runs (profile, skill_name);
CREATE INDEX ratchet_status_idx ON ratchet_runs (profile, status);

CREATE TRIGGER ratchet_set_updated_at
    BEFORE UPDATE ON ratchet_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
