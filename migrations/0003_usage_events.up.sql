-- 0003_usage_events — telemetry feeding the intelligence pipeline.
--
-- One row per observed usage event for a skill (loaded, helped, ignored-when-
-- relevant, errored). Agents batch these to POST /usage; the daemon dedups on
-- (profile, skill_name, session_id, event, ts) so the wqueue's immediate-attempt
-- + drainer double-POST doesn't double-count.

CREATE TABLE usage_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile     text NOT NULL,
    skill_name  text NOT NULL,
    session_id  text NOT NULL DEFAULT '',
    machine     text NOT NULL DEFAULT '',
    event       text NOT NULL,                 -- invoked|helpful|ignored|error
    context     jsonb,
    ts          timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT usage_event_chk CHECK (event IN ('invoked','helpful','ignored','error')),
    CONSTRAINT usage_dedup UNIQUE (profile, skill_name, session_id, event, ts)
);

CREATE INDEX usage_skill_idx ON usage_events (profile, skill_name);
CREATE INDEX usage_ts_idx    ON usage_events (profile, ts);
