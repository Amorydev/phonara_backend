-- ============================================================
-- Migration: 000006_user_avatar
-- Ảnh đại diện người dùng (hiển thị ở header Home)
-- ============================================================

ALTER TABLE users ADD COLUMN avatar_url TEXT;
