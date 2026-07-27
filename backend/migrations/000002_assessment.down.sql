-- Rollback migration 000002_assessment

DROP TABLE IF EXISTS assessment_questions CASCADE;
DROP TABLE IF EXISTS assessment_sets CASCADE;
