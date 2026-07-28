ALTER TABLE practice_sessions
  ADD COLUMN target_item_count INT
  CHECK (target_item_count IS NULL OR target_item_count > 0);

UPDATE app_configs
SET value = 'false'::jsonb
WHERE key = 'feature_exam';
