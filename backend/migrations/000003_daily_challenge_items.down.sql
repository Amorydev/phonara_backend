-- Rollback migration 000003_daily_challenge_items

DROP TABLE IF EXISTS daily_challenge_items CASCADE;

ALTER TABLE daily_challenges DROP COLUMN IF EXISTS title;
ALTER TABLE daily_challenges DROP COLUMN IF EXISTS description;
ALTER TABLE daily_challenges ADD COLUMN passage_id      UUID REFERENCES shadowing_passages(id) ON DELETE SET NULL;
ALTER TABLE daily_challenges ADD COLUMN content_item_id UUID REFERENCES content_items(id) ON DELETE SET NULL;
