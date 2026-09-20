-- 0003 added confidence as a nullable column, so every row written before that
-- migration carries NULL. The domain type is a plain float64, and pgx cannot
-- scan NULL into one - reading any pre-migration version returned a 500.
--
-- Zero is the right backfill: the code already treats 0 as "the model did not
-- report a confidence", which is exactly true of rows written before the field
-- existed.
--
-- General shape worth remembering: adding a nullable column and reading it into
-- a non-pointer Go type is always this bug. Either the column is NOT NULL with
-- a default, or the Go field is a pointer.
UPDATE chat_config_versions SET confidence = 0 WHERE confidence IS NULL;
ALTER TABLE chat_config_versions ALTER COLUMN confidence SET DEFAULT 0;
ALTER TABLE chat_config_versions ALTER COLUMN confidence SET NOT NULL;
