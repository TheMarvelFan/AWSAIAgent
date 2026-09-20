-- Users -----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id            UUID        PRIMARY KEY,
    email         TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    display_name  TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Emails are normalized to lowercase in the application; the functional index
-- makes that a database guarantee rather than a convention.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_key ON users (lower(email));

-- Refresh tokens --------------------------------------------------------
-- Only the SHA-256 of the token is stored. A leaked table dump cannot be
-- replayed against the API.
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  BYTEA       NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    replaced_by UUID,
    user_agent  TEXT        NOT NULL DEFAULT '',
    ip          TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS refresh_tokens_user_active_idx
    ON refresh_tokens (user_id) WHERE revoked_at IS NULL;

-- Chats -----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS chats (
    id                     UUID        PRIMARY KEY,
    user_id                UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title                  TEXT        NOT NULL DEFAULT '',
    -- Pointer to the running config shown in the right-hand panel. NULL until
    -- the first proposal exists.
    current_config_version INTEGER,
    -- Monotonic counter used to hand out message.seq inside the turn
    -- transaction. Doubles as the message count for the sidebar.
    message_seq            BIGINT      NOT NULL DEFAULT 0,
    last_message_at        TIMESTAMPTZ,
    archived_at            TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS chats_user_recent_idx
    ON chats (user_id, COALESCE(last_message_at, created_at) DESC);

-- Messages --------------------------------------------------------------
CREATE TABLE IF NOT EXISTS messages (
    id             UUID        PRIMARY KEY,
    chat_id        UUID        NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    seq            BIGINT      NOT NULL,
    role           TEXT        NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content        TEXT        NOT NULL,
    -- Set on the assistant turn that produced a new config version.
    config_version INTEGER,
    model          TEXT        NOT NULL DEFAULT '',
    input_tokens   INTEGER     NOT NULL DEFAULT 0,
    output_tokens  INTEGER     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, seq)
);

CREATE INDEX IF NOT EXISTS messages_chat_seq_idx ON messages (chat_id, seq DESC);

-- Config versions -------------------------------------------------------
-- Append-only. Rows are never updated or deleted; a revert writes a NEW row
-- whose document is a copy of the target version. History is therefore always
-- complete and every version stays diffable against every other.
CREATE TABLE IF NOT EXISTS chat_config_versions (
    id                    UUID        PRIMARY KEY,
    chat_id               UUID        NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    version               INTEGER     NOT NULL,
    parent_version        INTEGER,
    reverted_from_version INTEGER,
    source                TEXT        NOT NULL CHECK (source IN ('assistant', 'revert', 'manual')),
    document              JSONB       NOT NULL,
    summary               TEXT        NOT NULL DEFAULT '',
    rationale             TEXT        NOT NULL DEFAULT '',
    changes               JSONB       NOT NULL DEFAULT '[]'::jsonb,
    validation            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_by_message_id UUID        REFERENCES messages(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, version)
);

CREATE INDEX IF NOT EXISTS chat_config_versions_chat_idx
    ON chat_config_versions (chat_id, version DESC);
