-- AWS account connections -----------------------------------------------
--
-- One row per user. We never store an AWS credential here: role_arn is an
-- address and external_id is a secret WE generate. Neither grants access on
-- its own - the actual credentials are minted by sts:AssumeRole at the moment
-- of use and expire by themselves.
--
-- UNIQUE(user_id) enforces the "one AWS account per user" decision. Relaxing
-- it later means dropping this constraint and adding a connection reference to
-- chats; nothing else in the schema assumes it.
CREATE TABLE IF NOT EXISTS aws_connections (
    id              UUID        PRIMARY KEY,
    user_id         UUID        NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,

    -- Generated once at first connect and reused forever. Regenerating it on
    -- reconnect would silently break every assume, because the role created by
    -- CloudFormation still carries the original value.
    external_id     TEXT        NOT NULL,

    -- Deterministic from user_id, so a delete-and-recreate of the stack
    -- produces the identical ARN and repairs the connection with no action
    -- from us.
    role_name       TEXT        NOT NULL,

    -- NULL until the user comes back from CloudFormation with the stack
    -- output, and we have verified it against STS.
    role_arn        TEXT,
    account_id      TEXT,

    -- The single region this user's resources are provisioned into. Lives here
    -- rather than in the config document so it cannot come from model output.
    region          TEXT        NOT NULL,

    status          TEXT        NOT NULL CHECK (status IN ('pending', 'active', 'broken')),
    last_error      TEXT        NOT NULL DEFAULT '',
    verified_at     TIMESTAMPTZ,
    last_checked_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS aws_connections_status_idx ON aws_connections (status);
