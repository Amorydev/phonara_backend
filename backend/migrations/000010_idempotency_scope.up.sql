-- Idempotency keys are supplied by each authenticated client and are scoped to
-- that user. A global UNIQUE constraint lets one user's key collide with
-- another user's otherwise unrelated request.
ALTER TABLE assessment_jobs
  DROP CONSTRAINT IF EXISTS assessment_jobs_idempotency_key_key;

CREATE UNIQUE INDEX idx_assessment_jobs_user_idempotency
  ON assessment_jobs(user_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
