DROP INDEX IF EXISTS idx_pir_session_idempotency;
ALTER TABLE practice_item_results DROP COLUMN IF EXISTS idempotency_key;
