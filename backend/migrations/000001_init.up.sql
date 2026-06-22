-- ============================================================
-- Migration: 000001_init
-- Full schema from doc 13 — PostgreSQL 16
-- ============================================================

-- Extensions
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "citext";

-- Helper: auto-update updated_at
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ────────────────────────────────────────────────────────────
-- §2  Identity & Profile (M1)
-- ────────────────────────────────────────────────────────────

CREATE TABLE users (
  id                         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  auth_provider              TEXT        NOT NULL CHECK (auth_provider IN ('email','google','apple','guest')),
  email                      CITEXT      UNIQUE,
  external_auth_id           TEXT,
  password_hash              TEXT,                               -- email auth only
  display_name               TEXT,

  -- onboarding / profile
  goal                       TEXT        CHECK (goal IN ('communication','interview','ielts_toeic','beginner')),
  level                      TEXT        CHECK (level IN ('beginner','intermediate','advanced')),
  default_scoring_level      TEXT        NOT NULL DEFAULT 'medium'
                               CHECK (default_scoring_level IN ('easy','medium','hard')),
  target_accent              TEXT        DEFAULT 'US' CHECK (target_accent IN ('US','UK')),
  daily_goal_items           INT         DEFAULT 5,
  timezone                   TEXT        DEFAULT 'Asia/Ho_Chi_Minh',

  is_guest                   BOOLEAN     NOT NULL DEFAULT FALSE,
  onboarding_completed       BOOLEAN     NOT NULL DEFAULT FALSE,

  -- consent / privacy
  consent_store_recordings   BOOLEAN     NOT NULL DEFAULT TRUE,
  consent_improve_product    BOOLEAN     NOT NULL DEFAULT FALSE,

  -- notification preferences
  notif_practice_reminder    BOOLEAN     NOT NULL DEFAULT TRUE,
  notif_streak_reminder      BOOLEAN     NOT NULL DEFAULT TRUE,
  reminder_time              TIME,

  created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at                 TIMESTAMPTZ
);
CREATE INDEX idx_users_external_auth ON users(auth_provider, external_auth_id);
CREATE TRIGGER trg_users_updated BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Device / push token
CREATE TABLE user_devices (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform      TEXT        NOT NULL CHECK (platform IN ('ios','android')),
  push_token    TEXT,
  last_seen_at  TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, push_token)
);

-- Auth sessions / refresh tokens
CREATE TABLE auth_sessions (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_hash  TEXT        NOT NULL,
  device_id     UUID        REFERENCES user_devices(id) ON DELETE SET NULL,
  expires_at    TIMESTAMPTZ NOT NULL,
  revoked_at    TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_auth_sessions_user ON auth_sessions(user_id) WHERE revoked_at IS NULL;

-- ────────────────────────────────────────────────────────────
-- §3  Content (M5)
-- ────────────────────────────────────────────────────────────

CREATE TABLE l1_error_tags (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  code            TEXT        NOT NULL,
  name_vi         TEXT        NOT NULL,
  description_vi  TEXT,
  importance      INT         NOT NULL DEFAULT 0,
  version         INT         NOT NULL DEFAULT 1,
  is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (code, version)
);
CREATE TRIGGER trg_l1_error_tags_updated BEFORE UPDATE ON l1_error_tags
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE fix_guides (
  id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  l1_tag_id          UUID        REFERENCES l1_error_tags(id) ON DELETE SET NULL,
  phoneme            TEXT,
  tongue_position_vi TEXT,
  media_url          TEXT,
  examples           JSONB       DEFAULT '[]'::jsonb,
  version            INT         NOT NULL DEFAULT 1,
  is_active          BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (l1_tag_id IS NOT NULL OR phoneme IS NOT NULL)
);
CREATE INDEX idx_fix_guides_phoneme ON fix_guides(phoneme) WHERE is_active;
CREATE INDEX idx_fix_guides_l1      ON fix_guides(l1_tag_id) WHERE is_active;
CREATE TRIGGER trg_fix_guides_updated BEFORE UPDATE ON fix_guides
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE content_items (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  type           TEXT        NOT NULL CHECK (type IN ('word','sentence')),
  text           TEXT        NOT NULL,
  ipa            TEXT,
  audio_url_us   TEXT,
  audio_url_uk   TEXT,
  topic          TEXT,
  difficulty     INT         DEFAULT 1,
  target_goal    TEXT[]      DEFAULT '{}',
  focus_phonemes TEXT[]      DEFAULT '{}',
  version        INT         NOT NULL DEFAULT 1,
  is_active      BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_content_type_topic    ON content_items(type, topic) WHERE is_active;
CREATE INDEX idx_content_focus_phonemes ON content_items USING GIN (focus_phonemes);
CREATE INDEX idx_content_goal           ON content_items USING GIN (target_goal);
CREATE TRIGGER trg_content_items_updated BEFORE UPDATE ON content_items
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE content_item_l1_tags (
  content_item_id UUID NOT NULL REFERENCES content_items(id) ON DELETE CASCADE,
  l1_tag_id       UUID NOT NULL REFERENCES l1_error_tags(id) ON DELETE CASCADE,
  PRIMARY KEY (content_item_id, l1_tag_id)
);

CREATE TABLE minimal_pairs (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  word_a      TEXT        NOT NULL,
  word_b      TEXT        NOT NULL,
  phoneme_a   TEXT        NOT NULL,
  phoneme_b   TEXT        NOT NULL,
  audio_a_us  TEXT,
  audio_a_uk  TEXT,
  audio_b_us  TEXT,
  audio_b_uk  TEXT,
  explain_vi  TEXT,
  l1_tag_id   UUID        REFERENCES l1_error_tags(id) ON DELETE SET NULL,
  category    TEXT,
  difficulty  INT         DEFAULT 1,
  is_free     BOOLEAN     NOT NULL DEFAULT FALSE,
  version     INT         NOT NULL DEFAULT 1,
  is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_mp_l1 ON minimal_pairs(l1_tag_id) WHERE is_active;
CREATE TRIGGER trg_minimal_pairs_updated BEFORE UPDATE ON minimal_pairs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE shadowing_passages (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  title           TEXT        NOT NULL,
  source          TEXT        NOT NULL DEFAULT 'curated'
                    CHECK (source IN ('curated','daily','news','dialogue')),
  topic           TEXT,
  difficulty      INT         DEFAULT 1,
  sentence_count  INT         NOT NULL DEFAULT 0,
  version         INT         NOT NULL DEFAULT 1,
  is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_shadowing_passages_updated BEFORE UPDATE ON shadowing_passages
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE passage_sentences (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  passage_id       UUID        NOT NULL REFERENCES shadowing_passages(id) ON DELETE CASCADE,
  order_index      INT         NOT NULL,
  text             TEXT        NOT NULL,
  ipa              TEXT,
  native_audio_url TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (passage_id, order_index)
);

-- ────────────────────────────────────────────────────────────
-- §4  Practice & Scoring (M2/M3)
-- ────────────────────────────────────────────────────────────

CREATE TABLE practice_sessions (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  mode           TEXT        NOT NULL CHECK (mode IN
                   ('word','sentence','minimal_pair','read_word',
                    'shadowing','exam','conversation')),
  scoring_level  TEXT        NOT NULL DEFAULT 'medium'
                   CHECK (scoring_level IN ('easy','medium','hard')),
  source         TEXT        CHECK (source IN ('recommended','free_choice','daily','onboarding')),
  summary_score  REAL,
  started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at       TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user_time ON practice_sessions(user_id, started_at DESC);
CREATE INDEX idx_sessions_user_mode ON practice_sessions(user_id, mode);

CREATE TABLE practice_item_results (
  id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id          UUID        NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
  content_item_id     UUID        REFERENCES content_items(id) ON DELETE SET NULL,
  minimal_pair_id     UUID        REFERENCES minimal_pairs(id) ON DELETE SET NULL,
  passage_sentence_id UUID        REFERENCES passage_sentences(id) ON DELETE SET NULL,
  scoring_level       TEXT        NOT NULL CHECK (scoring_level IN ('easy','medium','hard')),
  accuracy            REAL,
  fluency             REAL,
  completeness        REAL,
  prosody             REAL,
  miscue              JSONB       DEFAULT '[]'::jsonb,
  audio_ref           TEXT,
  trust_flag          TEXT        DEFAULT 'ok'
                        CHECK (trust_flag IN ('ok','flagged','rejected')),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_item_results_session ON practice_item_results(session_id);
CREATE INDEX idx_item_results_content ON practice_item_results(content_item_id);

CREATE TABLE phoneme_scores (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  item_result_id   UUID        NOT NULL REFERENCES practice_item_results(id) ON DELETE CASCADE,
  expected_phoneme TEXT        NOT NULL,
  said_phoneme     TEXT,
  accuracy         REAL        NOT NULL,
  word_index       INT,
  phoneme_index    INT,
  is_omission      BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_phoneme_scores_item     ON phoneme_scores(item_result_id);
CREATE INDEX idx_phoneme_scores_expected ON phoneme_scores(expected_phoneme);

-- ────────────────────────────────────────────────────────────
-- §5  Error Profile & Coach (M4)
-- ────────────────────────────────────────────────────────────

CREATE TABLE error_profiles (
  id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             UUID        NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  top_errors          JSONB       DEFAULT '[]'::jsonb,
  last_recomputed_at  TIMESTAMPTZ,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_error_profiles_updated BEFORE UPDATE ON error_profiles
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE phoneme_mastery (
  id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  error_profile_id  UUID        NOT NULL REFERENCES error_profiles(id) ON DELETE CASCADE,
  phoneme           TEXT        NOT NULL,
  mastery           REAL        NOT NULL DEFAULT 0,
  attempts          INT         NOT NULL DEFAULT 0,
  status            TEXT        NOT NULL DEFAULT 'weak'
                      CHECK (status IN ('weak','improving','good')),
  last_practiced_at TIMESTAMPTZ,
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (error_profile_id, phoneme)
);
CREATE INDEX idx_phoneme_mastery_status ON phoneme_mastery(error_profile_id, status);

CREATE TABLE pair_mastery (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  error_profile_id UUID        NOT NULL REFERENCES error_profiles(id) ON DELETE CASCADE,
  minimal_pair_id  UUID        NOT NULL REFERENCES minimal_pairs(id) ON DELETE CASCADE,
  listen_mastery   REAL        NOT NULL DEFAULT 0,
  speak_mastery    REAL        NOT NULL DEFAULT 0,
  attempts         INT         NOT NULL DEFAULT 0,
  status           TEXT        NOT NULL DEFAULT 'weak'
                     CHECK (status IN ('weak','improving','good')),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (error_profile_id, minimal_pair_id)
);

CREATE TABLE skill_mastery (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  error_profile_id UUID        NOT NULL REFERENCES error_profiles(id) ON DELETE CASCADE,
  skill            TEXT        NOT NULL CHECK (skill IN
                     ('prosody','fluency','completeness','accuracy','final_consonant')),
  mastery          REAL        NOT NULL DEFAULT 0,
  status           TEXT        NOT NULL DEFAULT 'weak'
                     CHECK (status IN ('weak','improving','good')),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (error_profile_id, skill)
);

-- ────────────────────────────────────────────────────────────
-- §6  Shadowing Progress (M9, P2)
-- ────────────────────────────────────────────────────────────

CREATE TABLE shadowing_progress (
  id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  passage_id             UUID        NOT NULL REFERENCES shadowing_passages(id) ON DELETE CASCADE,
  current_sentence_index INT         NOT NULL DEFAULT 0,
  sentence_status        JSONB       DEFAULT '[]'::jsonb,
  attempts_per_sentence  JSONB       DEFAULT '{}'::jsonb,
  passage_avg_score      REAL,
  completed              BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, passage_id)
);
CREATE TRIGGER trg_shadowing_progress_updated BEFORE UPDATE ON shadowing_progress
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ────────────────────────────────────────────────────────────
-- §7  Streak & Gamification (M8) + Daily (M10)
-- ────────────────────────────────────────────────────────────

CREATE TABLE streak_records (
  user_id          UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  current_streak   INT         NOT NULL DEFAULT 0,
  longest_streak   INT         NOT NULL DEFAULT 0,
  last_active_date DATE,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_streak_records_updated BEFORE UPDATE ON streak_records
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE daily_progress (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  date        DATE        NOT NULL,
  items_done  INT         NOT NULL DEFAULT 0,
  goal_met    BOOLEAN     NOT NULL DEFAULT FALSE,
  xp_earned   INT         NOT NULL DEFAULT 0,
  UNIQUE (user_id, date)
);
CREATE INDEX idx_daily_progress_user ON daily_progress(user_id, date DESC);

CREATE TABLE badges (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  code           TEXT        NOT NULL UNIQUE,
  name_vi        TEXT        NOT NULL,
  description_vi TEXT,
  icon_url       TEXT,
  criteria_type  TEXT        NOT NULL CHECK (criteria_type IN
                   ('streak','phoneme_mastered','items_done','pairs_mastered')),
  criteria_value INT         NOT NULL,
  criteria_ref   TEXT,
  is_active      BOOLEAN     NOT NULL DEFAULT TRUE
);

CREATE TABLE user_badges (
  user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  badge_id   UUID        NOT NULL REFERENCES badges(id) ON DELETE CASCADE,
  earned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, badge_id)
);

CREATE TABLE daily_challenges (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  date            DATE        NOT NULL UNIQUE,
  passage_id      UUID        REFERENCES shadowing_passages(id) ON DELETE SET NULL,
  content_item_id UUID        REFERENCES content_items(id) ON DELETE SET NULL,
  category        TEXT,
  banner_url      TEXT,
  moderated       BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE daily_challenge_completions (
  user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  challenge_id UUID        NOT NULL REFERENCES daily_challenges(id) ON DELETE CASCADE,
  status       TEXT        NOT NULL CHECK (status IN ('completed','missed','in_progress')),
  score        REAL,
  completed_at TIMESTAMPTZ,
  PRIMARY KEY (user_id, challenge_id)
);

CREATE TABLE mastery_snapshots (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  snapshot_date   DATE        NOT NULL,
  overall_score   REAL,
  phoneme_mastery JSONB,
  skill_mastery   JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, snapshot_date)
);
CREATE INDEX idx_mastery_snapshots_user ON mastery_snapshots(user_id, snapshot_date DESC);

-- ────────────────────────────────────────────────────────────
-- §8  Subscription & IAP (M7)
-- ────────────────────────────────────────────────────────────

CREATE TABLE subscriptions (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID        NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  plan        TEXT        NOT NULL DEFAULT 'free'
                CHECK (plan IN ('free','premium','exam_pack')),
  status      TEXT        NOT NULL DEFAULT 'active'
                CHECK (status IN ('active','canceled','expired','in_grace')),
  store       TEXT        CHECK (store IN ('app_store','google_play')),
  renews_at   TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_subscriptions_updated BEFORE UPDATE ON subscriptions
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE iap_transactions (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  store           TEXT        NOT NULL CHECK (store IN ('app_store','google_play')),
  product_id      TEXT        NOT NULL,
  original_txn_id TEXT,
  raw_receipt     JSONB,
  status          TEXT        NOT NULL CHECK (status IN ('verified','refunded','failed')),
  verified_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (store, original_txn_id)
);

CREATE TABLE freemium_usage (
  user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  date        DATE        NOT NULL,
  items_used  INT         NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, date)
);

CREATE TABLE plan_configs (
  id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  plan                TEXT        NOT NULL CHECK (plan IN ('premium','exam_pack')),
  product_id_ios      TEXT,
  product_id_android  TEXT,
  billing_period      TEXT        CHECK (billing_period IN ('monthly','yearly','one_time')),
  price_vnd           INT,
  display_name_vi     TEXT,
  features_vi         JSONB,
  is_active           BOOLEAN     NOT NULL DEFAULT TRUE,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_plan_configs_updated BEFORE UPDATE ON plan_configs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ────────────────────────────────────────────────────────────
-- §9  Exam (M13, P3)
-- ────────────────────────────────────────────────────────────

CREATE TABLE exam_prompts (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  exam_type     TEXT        NOT NULL CHECK (exam_type IN ('ielts','toeic')),
  part          TEXT        NOT NULL,
  prompt_text   TEXT        NOT NULL,
  prep_seconds  INT         DEFAULT 0,
  speak_seconds INT         DEFAULT 0,
  version       INT         NOT NULL DEFAULT 1,
  is_active     BOOLEAN     NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE exam_sessions (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  prompt_id   UUID        REFERENCES exam_prompts(id) ON DELETE SET NULL,
  exam_type   TEXT        NOT NULL CHECK (exam_type IN ('ielts','toeic')),
  audio_ref   TEXT,
  band_score  REAL,
  criteria    JSONB,
  cefr_level  TEXT,
  status      TEXT        NOT NULL DEFAULT 'submitted'
                CHECK (status IN ('submitted','scoring','scored','failed')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  scored_at   TIMESTAMPTZ
);
CREATE INDEX idx_exam_sessions_user ON exam_sessions(user_id, created_at DESC);

-- ────────────────────────────────────────────────────────────
-- §10  Notifications & Analytics
-- ────────────────────────────────────────────────────────────

CREATE TABLE notifications (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type        TEXT        NOT NULL CHECK (type IN ('streak_reminder','weak_point','daily','renewal')),
  payload     JSONB,
  sent_at     TIMESTAMPTZ,
  read_at     TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications(user_id, created_at DESC);

CREATE TABLE analytics_events (
  id          BIGSERIAL   PRIMARY KEY,
  user_id     UUID        REFERENCES users(id) ON DELETE SET NULL,
  event_name  TEXT        NOT NULL,
  properties  JSONB,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_analytics_name_time ON analytics_events(event_name, created_at DESC);

CREATE TABLE feedback_reports (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID        REFERENCES users(id) ON DELETE SET NULL,
  type        TEXT        NOT NULL CHECK (type IN ('feedback','bug','support')),
  message     TEXT        NOT NULL,
  context     JSONB,
  status      TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_feedback_status ON feedback_reports(status, created_at DESC);

-- ────────────────────────────────────────────────────────────
-- §10b App config & Legal
-- ────────────────────────────────────────────────────────────

CREATE TABLE app_configs (
  key        TEXT  PRIMARY KEY,
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_app_configs_updated BEFORE UPDATE ON app_configs
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE legal_documents (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  doc_type    TEXT        NOT NULL CHECK (doc_type IN ('terms','privacy')),
  version     INT         NOT NULL,
  content_md  TEXT        NOT NULL,
  locale      TEXT        NOT NULL DEFAULT 'vi',
  published_at TIMESTAMPTZ,
  UNIQUE (doc_type, version, locale)
);

-- ────────────────────────────────────────────────────────────
-- §10c Minimal Pairs Listen drill
-- ────────────────────────────────────────────────────────────

CREATE TABLE mp_listen_drills (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  total_items   INT         NOT NULL,
  correct_count INT         NOT NULL DEFAULT 0,
  hearts_left   INT         NOT NULL DEFAULT 3,
  status        TEXT        NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active','completed','failed')),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at  TIMESTAMPTZ
);

CREATE TABLE mp_listen_answers (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  drill_id        UUID        NOT NULL REFERENCES mp_listen_drills(id) ON DELETE CASCADE,
  minimal_pair_id UUID        NOT NULL REFERENCES minimal_pairs(id) ON DELETE CASCADE,
  played_word     TEXT        NOT NULL,
  chosen_word     TEXT,
  is_correct      BOOLEAN,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed default app configs
INSERT INTO app_configs (key, value) VALUES
  ('min_version_ios', '"1.0.0"'),
  ('min_version_android', '"1.0.0"'),
  ('feature_conversation', 'false'),
  ('feature_exam', 'true'),
  ('maintenance_mode', 'false')
ON CONFLICT DO NOTHING;
