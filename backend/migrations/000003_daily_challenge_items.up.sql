-- ============================================================
-- Migration: 000003_daily_challenge_items
-- Daily Challenge → bộ nhiều mục (production)
-- ============================================================
-- Trước đây mỗi ngày chỉ trỏ tới 1 passage HOẶC 1 content_item.
-- Production: một challenge là một BỘ gồm nhiều mục có thứ tự + chủ đề,
-- để /daily/today trả về toàn bộ nội dung trong một request.

-- Header của challenge: thêm tiêu đề/mô tả cho màn hình; bỏ 2 cột trỏ đơn lẻ
-- (chuyển sang bảng items bên dưới).
ALTER TABLE daily_challenges ADD COLUMN title       TEXT;
ALTER TABLE daily_challenges ADD COLUMN description TEXT;
ALTER TABLE daily_challenges DROP COLUMN passage_id;
ALTER TABLE daily_challenges DROP COLUMN content_item_id;

-- Mỗi mục trong bộ challenge của một ngày. Trỏ tới content_item (word/sentence)
-- hoặc shadowing_passage. order_index quyết định thứ tự hiển thị.
CREATE TABLE daily_challenge_items (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  challenge_id    UUID        NOT NULL REFERENCES daily_challenges(id) ON DELETE CASCADE,
  order_index     INT         NOT NULL,
  kind            TEXT        NOT NULL CHECK (kind IN ('word','sentence','passage')),
  content_item_id UUID        REFERENCES content_items(id) ON DELETE CASCADE,
  passage_id      UUID        REFERENCES shadowing_passages(id) ON DELETE CASCADE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (challenge_id, order_index),
  -- đúng một loại tham chiếu được set
  CHECK ((content_item_id IS NOT NULL) <> (passage_id IS NOT NULL))
);
CREATE INDEX idx_daily_challenge_items_challenge
  ON daily_challenge_items(challenge_id, order_index);
