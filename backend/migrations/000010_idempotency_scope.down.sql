DROP INDEX IF EXISTS idx_assessment_jobs_user_idempotency;

ALTER TABLE assessment_jobs
  ADD CONSTRAINT assessment_jobs_idempotency_key_key UNIQUE (idempotency_key);
