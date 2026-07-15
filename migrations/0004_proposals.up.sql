-- 0004_proposals — the create/prune/promote queue (the human gate).
--
-- Every create/prune/promote is a proposal that stays 'pending' until an
-- operator (or an autonomous opt-in) approves or rejects it. Approving applies
-- the change to the registry; nothing mutates skills without a decision here.

CREATE TABLE proposals (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile              text NOT NULL,
    kind                 text NOT NULL,               -- create|prune|promote
    skill_name           text NOT NULL,
    proposed_frontmatter jsonb,
    proposed_body        text,
    diff                 text,
    rationale            text NOT NULL DEFAULT '',
    evidence             jsonb,                        -- pipeline-produced
    score                double precision,             -- pipeline scorer output
    verifier_report      jsonb,                        -- Verifier output
    status               text NOT NULL DEFAULT 'pending', -- pending|approved|rejected
    created_by           text NOT NULL DEFAULT '',     -- agent session, pipeline, operator
    decided_by           text,
    decided_reason       text,
    decided_at           timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT proposal_kind_chk   CHECK (kind IN ('create','prune','promote')),
    CONSTRAINT proposal_status_chk CHECK (status IN ('pending','approved','rejected'))
);

CREATE INDEX proposals_status_idx ON proposals (profile, status);
CREATE INDEX proposals_kind_idx   ON proposals (profile, kind);
