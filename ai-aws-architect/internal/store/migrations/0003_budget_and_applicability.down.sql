ALTER TABLE aws_connections DROP COLUMN IF EXISTS last_error_code;
DROP INDEX IF EXISTS chat_config_versions_applicable_idx;
ALTER TABLE chat_config_versions
    DROP CONSTRAINT IF EXISTS chat_config_versions_scope_check,
    DROP COLUMN IF EXISTS confidence,
    DROP COLUMN IF EXISTS scope,
    DROP COLUMN IF EXISTS blockers,
    DROP COLUMN IF EXISTS applicable;
ALTER TABLE chats DROP COLUMN IF EXISTS monthly_budget_usd;
