-- Budget ----------------------------------------------------------------
-- A number, not a tier. "Student" vs "professional" encodes assumptions that
-- do not hold (students have credits, professionals are cost-sensitive) and
-- would be mapped back to a number anyway.
ALTER TABLE chats
    ADD COLUMN IF NOT EXISTS monthly_budget_usd NUMERIC(10, 2);

-- Applicability ---------------------------------------------------------
-- A config version now has three possible fates, not two:
--
--   stored + applicable      normal proposal, Apply is available
--   stored + NOT applicable  shown in the panel, Apply disabled, reason given
--   not stored at all        the model produced something invalid; a bug
--
-- The middle state is what the spec's confidence/scope guardrail actually
-- requires: "still shows a proposed architecture and code, but does not
-- execute it, and says so explicitly". Without it, a request the catalog
-- cannot cover is indistinguishable from a model fault.
ALTER TABLE chat_config_versions
    ADD COLUMN IF NOT EXISTS applicable BOOLEAN NOT NULL DEFAULT true,
    -- Why it cannot be applied. Empty when applicable.
    ADD COLUMN IF NOT EXISTS blockers JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- The model's own read on whether it understood the request. Distinct from
    -- validation, which only answers "is this legal".
    ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'supported',
    -- Self-reported by the model and NOT calibrated: 0.9 does not mean 90% of
    -- these are correct. Surfaced to developers as a hint, never used as a
    -- threshold in code.
    ADD COLUMN IF NOT EXISTS confidence REAL;

ALTER TABLE chat_config_versions
    DROP CONSTRAINT IF EXISTS chat_config_versions_scope_check;
ALTER TABLE chat_config_versions
    ADD CONSTRAINT chat_config_versions_scope_check
    CHECK (scope IN ('supported', 'needs_clarification', 'out_of_scope'));

CREATE INDEX IF NOT EXISTS chat_config_versions_applicable_idx
    ON chat_config_versions (chat_id, applicable) WHERE NOT applicable;

-- Connection failure classification -------------------------------------
-- The message alone cannot be branched on. The code lets the UI decide
-- between "check your AWS account" and "AWS itself looks unhealthy", which
-- are different problems with different next steps.
ALTER TABLE aws_connections
    ADD COLUMN IF NOT EXISTS last_error_code TEXT NOT NULL DEFAULT '';
