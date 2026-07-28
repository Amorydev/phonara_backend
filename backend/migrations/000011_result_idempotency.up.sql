-- Persist idempotency with the result itself. A Redis reservation made before
-- the database transaction can outlive a failed write and turn retries into
-- false successes.
ALTER TABLE practice_item_results ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX idx_pir_session_idempotency
  ON practice_item_results(session_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
