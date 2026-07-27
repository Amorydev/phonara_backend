-- ============================================================
-- Migration: 000004_daily_mission
-- Nhiệm vụ hàng ngày — mục tiêu thời lượng luyện tập (phút)
-- ============================================================
-- Goal tính theo PHÚT (tách khỏi daily_goal_items vốn theo số mục).
-- Thời gian active do client gửi lên (heartbeat), tích lũy theo ngày.

ALTER TABLE users ADD COLUMN daily_goal_minutes INT NOT NULL DEFAULT 15;

-- Tái dùng bảng daily_progress (đang trống): lưu GIÂY cho chính xác, expose PHÚT.
ALTER TABLE daily_progress ADD COLUMN seconds_practiced INT NOT NULL DEFAULT 0;
