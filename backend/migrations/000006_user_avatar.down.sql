-- Rollback migration 000006_user_avatar

ALTER TABLE users DROP COLUMN IF EXISTS avatar_url;
