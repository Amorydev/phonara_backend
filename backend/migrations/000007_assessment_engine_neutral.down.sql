-- Rollback migration 000007_assessment_engine_neutral

-- Khôi phục NOT NULL. Bản ghi có accuracy NULL (omission) phải được lấp trước,
-- nếu không ALTER sẽ thất bại — dùng 0 vì đó chính là giá trị schema cũ ép ta phải bịa.
UPDATE phoneme_scores SET accuracy = 0 WHERE accuracy IS NULL;
ALTER TABLE phoneme_scores ALTER COLUMN accuracy SET NOT NULL;

DROP INDEX IF EXISTS idx_phoneme_scores_diagnosis;
ALTER TABLE phoneme_scores
  DROP COLUMN IF EXISTS diagnosis,
  DROP COLUMN IF EXISTS confidence,
  DROP COLUMN IF EXISTS gop_raw;

DROP INDEX IF EXISTS idx_pir_engine;
DROP INDEX IF EXISTS idx_pir_assessment_question;
ALTER TABLE practice_item_results
  DROP COLUMN IF EXISTS assessment_job_id,
  DROP COLUMN IF EXISTS assessment_question_id,
  DROP COLUMN IF EXISTS capabilities,
  DROP COLUMN IF EXISTS calibration_version,
  DROP COLUMN IF EXISTS algorithm_version,
  DROP COLUMN IF EXISTS g2p_version,
  DROP COLUMN IF EXISTS model_version,
  DROP COLUMN IF EXISTS engine;

DROP TABLE IF EXISTS assessment_jobs CASCADE;

COMMENT ON COLUMN phoneme_scores.is_omission IS NULL;
