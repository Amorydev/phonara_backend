-- ============================================================
-- Migration: 000005_practice_modes
-- Card chế độ luyện tập ở Home (DB-driven, cấu hình bởi content team)
-- ============================================================
-- Mỗi card là một entry điều hướng tĩnh (giống cho mọi user). Có thể
-- bật/tắt, đổi thứ tự, gắn cờ premium mà không cần deploy lại.

CREATE TABLE practice_modes (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  key         TEXT        NOT NULL UNIQUE,   -- word, sentence, minimal_pair, shadowing, flashcard, profile
  title_vi    TEXT        NOT NULL,
  subtitle_vi TEXT,
  icon        TEXT,
  route       TEXT        NOT NULL,          -- deep-link client điều hướng tới
  is_premium  BOOLEAN     NOT NULL DEFAULT FALSE,
  order_index INT         NOT NULL,
  is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_practice_modes_active ON practice_modes(order_index) WHERE is_active;
CREATE TRIGGER trg_practice_modes_updated BEFORE UPDATE ON practice_modes
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
