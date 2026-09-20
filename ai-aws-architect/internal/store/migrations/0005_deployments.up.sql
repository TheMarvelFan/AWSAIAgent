-- Deployments -----------------------------------------------------------
--
-- This table is the answer to "what is actually deployed", which until now had
-- nowhere to live. chats.current_config_version means "what the conversation
-- currently proposes" - the panel on the right. It is NOT the same as what
-- exists in AWS, and conflating the two is how a system ends up telling someone
-- their infrastructure matches a config it has never applied.
--
-- One row per apply attempt, so the history records what was deployed when.
CREATE TABLE IF NOT EXISTS deployments (
    id             UUID        PRIMARY KEY,
    chat_id        UUID        NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    user_id        UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    config_version INTEGER     NOT NULL,

    status TEXT NOT NULL CHECK (status IN (
        'planning',          -- plan queued or running
        'awaiting_approval', -- plan done, a human must look at it
        'applying',
        'applied',           -- resources exist
        'superseded',        -- a later deployment replaced this one
        'plan_failed',
        'apply_failed',      -- PARTIAL resources may exist
        'destroying',
        'destroyed',
        'destroy_failed',    -- resources may remain and keep billing
        'cancelled',
        -- We do not know what exists. Reached when a run times out, when AWS
        -- returns an indeterminate error, or when the process died mid-apply.
        -- Never resolved by guessing: the only way out is a fresh plan.
        'unknown'
    )),

    -- Approval binds to a SPECIFIC plan, not just to a config version.
    -- Without the hash, a user could approve one plan and have a different one
    -- applied if anything changed in between.
    plan_hash    TEXT  NOT NULL DEFAULT '',
    plan_output  TEXT  NOT NULL DEFAULT '',
    plan_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    approved_at  TIMESTAMPTZ,
    approved_by  UUID REFERENCES users(id),

    -- Snapshotted at plan time. The connection can be revoked later, and this
    -- records where the resources actually went.
    aws_account_id TEXT NOT NULL DEFAULT '',
    aws_role_arn   TEXT NOT NULL DEFAULT '',
    region         TEXT NOT NULL DEFAULT '',
    -- One Terraform state per chat: that is what makes version 5 a
    -- modification of version 4 rather than a fresh build.
    state_key TEXT NOT NULL,

    failure_code    TEXT NOT NULL DEFAULT '',
    failure_message TEXT NOT NULL DEFAULT '',

    teardown_after TIMESTAMPTZ,
    applied_at     TIMESTAMPTZ,
    destroyed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one deployment per chat doing something. Two concurrent Terraform
-- runs against one state file corrupt it, and this is the database-level
-- guarantee rather than a promise made in Go.
CREATE UNIQUE INDEX IF NOT EXISTS deployments_one_inflight_per_chat
    ON deployments (chat_id)
    WHERE status IN ('planning', 'awaiting_approval', 'applying', 'destroying');

CREATE INDEX IF NOT EXISTS deployments_chat_idx ON deployments (chat_id, created_at DESC);
CREATE INDEX IF NOT EXISTS deployments_live_idx ON deployments (chat_id)
    WHERE status IN ('applied', 'apply_failed', 'destroy_failed', 'unknown');

-- Jobs ------------------------------------------------------------------
--
-- One job is one whole terraform run, NOT one resource. Terraform builds its
-- own dependency graph and parallelizes internally; scheduling per resource
-- would mean reimplementing that badly and would need a state file per
-- resource, which destroys the modification-not-recreation property.
--
-- In Postgres rather than an in-memory queue because a crashed process loses an
-- in-memory job with no record it ever existed, while AWS may hold half a VPC
-- by then. A row survives and says something was in flight.
CREATE TABLE IF NOT EXISTS jobs (
    id            UUID        PRIMARY KEY,
    type          TEXT        NOT NULL CHECK (type IN ('plan', 'apply', 'destroy')),
    deployment_id UUID        NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    -- Denormalized so the claim query can serialize per chat without a join.
    chat_id UUID NOT NULL,

    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'unknown')),

    attempts     INTEGER     NOT NULL DEFAULT 0,
    max_attempts INTEGER     NOT NULL DEFAULT 3,
    -- Doubles as the retry backoff and the auto-teardown scheduler: a destroy
    -- job with run_after set 45 minutes out is the whole teardown timer.
    run_after TIMESTAMPTZ NOT NULL DEFAULT now(),

    claimed_by   TEXT NOT NULL DEFAULT '',
    -- Refreshed while a job runs. A stale heartbeat means the worker died, and
    -- the job is moved to 'unknown' rather than 'failed' - the work may have
    -- partially completed.
    heartbeat_at TIMESTAMPTZ,
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,

    failure_code TEXT NOT NULL DEFAULT '',
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS jobs_claim_idx ON jobs (run_after) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS jobs_deployment_idx ON jobs (deployment_id, created_at DESC);
CREATE INDEX IF NOT EXISTS jobs_running_idx ON jobs (heartbeat_at) WHERE status = 'running';

-- Belt and braces behind the claim query's NOT EXISTS check. The subquery
-- cannot lock rows it is only testing for absence, so in a multi-worker race
-- two workers could both conclude a chat is idle. This index turns that race
-- into a unique violation the worker retries, instead of two Terraform runs.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_one_running_per_chat
    ON jobs (chat_id) WHERE status = 'running';
