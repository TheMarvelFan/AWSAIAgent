ALTER TABLE chat_config_versions ALTER COLUMN confidence DROP NOT NULL;
ALTER TABLE chat_config_versions ALTER COLUMN confidence DROP DEFAULT;