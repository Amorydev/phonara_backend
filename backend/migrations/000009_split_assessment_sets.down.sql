-- Rollback migration 000009_split_assessment_sets
DROP INDEX IF EXISTS idx_assessment_sets_one_default;
ALTER TABLE assessment_sets DROP COLUMN IF EXISTS is_default;
