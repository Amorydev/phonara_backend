-- Rollback migration 000004_daily_mission

ALTER TABLE daily_progress DROP COLUMN IF EXISTS seconds_practiced;
ALTER TABLE users DROP COLUMN IF EXISTS daily_goal_minutes;
