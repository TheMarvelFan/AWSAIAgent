-- Confidence becomes nullable again, reversing 0004.
--
-- 0004 made it NOT NULL DEFAULT 0 because the Go field was a plain float64 and
-- pgx cannot scan NULL into one. That was the right fix for the field as it
-- was. The field is now *float64, which scans NULL correctly, so the column can
-- express what the value actually means: a version no model produced has no
-- confidence, and 0 is a different claim from "none".
--
-- Without this, absence cannot survive a round trip. A revert of a manually
-- edited version writes 0 and reads back 0, and the API reports a confidence of
-- zero for a document nothing ever scored.
ALTER TABLE chat_config_versions ALTER COLUMN confidence DROP NOT NULL;
ALTER TABLE chat_config_versions ALTER COLUMN confidence DROP DEFAULT;

-- Manual versions never had a confidence; the 0 was 0004's storage artifact.
UPDATE chat_config_versions SET confidence = NULL WHERE source = 'manual';
