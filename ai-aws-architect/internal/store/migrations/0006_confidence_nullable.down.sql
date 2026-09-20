UPDATE chat_config_versions SET confidence = 0 WHERE confidence IS NULL;
ALTER TABLE chat_config_versions ALTER COLUMN confidence SET DEFAULT 0;
ALTER TABLE chat_config_versions ALTER COLUMN confidence SET NOT NULL;
