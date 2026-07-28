ALTER TABLE practice_sessions
  DROP COLUMN IF EXISTS target_item_count;

UPDATE app_configs
SET value = 'true'::jsonb
WHERE key = 'feature_exam';
