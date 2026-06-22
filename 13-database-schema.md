# 13 — Database Schema (PostgreSQL) — Full P1+P2+P3 (trừ Conversation Live)

> Trạng thái: Draft v0.1 · Liên quan: `08-data-model.md` (conceptual), `06-business-rules.md`, `12-backend-architecture-full.md`
>
> **Mục tiêu:** Schema vật lý đầy đủ, ổn định, **ít phải đổi sau** — phủ mọi entity của P1+P2+P3 (trừ Conversation Live nhưng để sẵn slot).

---

## 0. Quy ước & quyết định thiết kế (để ít thay đổi sau)

| Quyết định | Lý do |
|---|---|
| **PK = UUID** (`uuid_generate_v4()` / `gen_random_uuid()`) | Đa thiết bị, sync, không lộ thứ tự (NFR-REL-04) |
| **Enum = `TEXT` + `CHECK`** thay vì native `ENUM` | Thêm giá trị mới KHÔNG cần `ALTER TYPE` rủi ro (vd thêm mode `conversation` sau) |
| **`jsonb`** cho dữ liệu bán cấu trúc (top_errors, miscue, sentence_status) | Linh hoạt, không phải migrate khi đổi cấu trúc nhỏ |
| **`created_at` / `updated_at`** mọi bảng (trigger auto-update) | Audit, debug, sync |
| **Soft-delete** (`deleted_at`) cho bảng có dữ liệu người dùng | Xóa account vẫn audit được; hard-delete audio theo BR-PRIV |
| **`mode` enum đã gồm `conversation`** | Để sẵn slot, không phải migrate khi build Conversation |
| **Tách điểm gộp vs phoneme-level** | BR-SCORE-06: luôn lưu phoneme dù Level nào |
| **Content versioned** (`version`, `is_active`) | NFR-MAINT-02, linguist quản lý, không xóa cứng |
| **Index có chủ đích** cho query nóng | Recommendation, history, streak, leaderboard |
| **Timezone**: lưu `timestamptz` (UTC), streak tính theo `user.timezone` | BR-STREAK-01 |
| **Tiền/điểm**: dùng `numeric`/`real` đúng ngữ nghĩa | Mastery/score = `real` (0–100); tránh float lỗi tích lũy thì EWMA chấp nhận `real` |

---

## 1. Extensions & helper

```sql
CREATE EXTENSION IF NOT EXISTS "pgcrypto";   -- gen_random_uuid()

-- trigger tự cập nhật updated_at
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$ LANGUAGE plpgsql;
```

---

## 2. Identity & Profile (M1)

```sql
CREATE TABLE users (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  auth_provider         TEXT NOT NULL CHECK (auth_provider IN ('email','google','apple','guest')),
  email                 CITEXT UNIQUE,                  -- nullable cho guest
  external_auth_id      TEXT,                           -- sub của Google/Apple
  display_name          TEXT,

  -- onboarding / profile (FR-ONB, FR-ACC-05)
  goal                  TEXT CHECK (goal IN ('communication','interview','ielts_toeic','beginner')),
  level                 TEXT CHECK (level IN ('beginner','intermediate','advanced')),
  default_scoring_level TEXT NOT NULL DEFAULT 'medium'
                          CHECK (default_scoring_level IN ('easy','medium','hard')),  -- BR-LEVEL-06
  target_accent         TEXT DEFAULT 'US' CHECK (target_accent IN ('US','UK')),
  daily_goal_items      INT  DEFAULT 5,                 -- số bài/ngày (streak)
  timezone              TEXT DEFAULT 'Asia/Ho_Chi_Minh',-- BR-STREAK-01

  is_guest              BOOLEAN NOT NULL DEFAULT FALSE,
  onboarding_completed  BOOLEAN NOT NULL DEFAULT FALSE,

  -- consent / privacy (BR-PRIV-01/03, S23) — chi phối pipeline lưu audio
  consent_store_recordings   BOOLEAN NOT NULL DEFAULT TRUE,   -- lưu lịch sử bản ghi
  consent_improve_product    BOOLEAN NOT NULL DEFAULT FALSE,  -- dùng ghi âm cải thiện SP (opt-in)

  -- notification preferences tách theo loại (S08/S22)
  notif_practice_reminder    BOOLEAN NOT NULL DEFAULT TRUE,
  notif_streak_reminder      BOOLEAN NOT NULL DEFAULT TRUE,
  reminder_time              TIME,                            -- giờ nhắc (theo timezone)

  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at            TIMESTAMPTZ                       -- soft-delete (BR-PRIV-02)
);
CREATE INDEX idx_users_external_auth ON users(auth_provider, external_auth_id);
CREATE TRIGGER trg_users_updated BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- thiết bị (đa thiết bị + push token)
CREATE TABLE user_devices (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform      TEXT NOT NULL CHECK (platform IN ('ios','android')),
  push_token    TEXT,                                   -- APNs/FCM (M12)
  last_seen_at  TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, push_token)
);

-- refresh token / phiên (auth)
CREATE TABLE auth_sessions (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_hash  TEXT NOT NULL,                          -- hash, không lưu plaintext
  device_id     UUID REFERENCES user_devices(id) ON DELETE SET NULL,
  expires_at    TIMESTAMPTZ NOT NULL,
  revoked_at    TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_auth_sessions_user ON auth_sessions(user_id) WHERE revoked_at IS NULL;
```

---

## 3. Content (M5) — versioned, tách khỏi code

```sql
-- Taxonomy lỗi người Việt (BR-L1, versioned)
CREATE TABLE l1_error_tags (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code            TEXT NOT NULL,                         -- final_consonant_deletion, th_sound, v_w, l_n...
  name_vi         TEXT NOT NULL,
  description_vi  TEXT,
  importance      INT NOT NULL DEFAULT 0,                -- ưu tiên đề xuất (BR-COACH-04)
  version         INT NOT NULL DEFAULT 1,                -- BR-L1-05
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (code, version)
);

-- Nội dung "cách sửa" tiếng Việt (FR-WORD-05)
CREATE TABLE fix_guides (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  l1_tag_id          UUID REFERENCES l1_error_tags(id) ON DELETE SET NULL,
  phoneme            TEXT,                               -- gắn theo âm hoặc theo l1_tag
  tongue_position_vi TEXT,
  media_url          TEXT,                               -- animation/video
  examples           JSONB DEFAULT '[]'::jsonb,          -- ví dụ VN/EN
  version            INT NOT NULL DEFAULT 1,
  is_active          BOOLEAN NOT NULL DEFAULT TRUE,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (l1_tag_id IS NOT NULL OR phoneme IS NOT NULL)
);
CREATE INDEX idx_fix_guides_phoneme ON fix_guides(phoneme) WHERE is_active;
CREATE INDEX idx_fix_guides_l1 ON fix_guides(l1_tag_id) WHERE is_active;

-- ContentItem (word/sentence)
CREATE TABLE content_items (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  type          TEXT NOT NULL CHECK (type IN ('word','sentence')),
  text          TEXT NOT NULL,
  ipa           TEXT,
  audio_url_us  TEXT,                                    -- TTS cached (BR-CONT-01)
  audio_url_uk  TEXT,
  topic         TEXT,                                    -- phỏng vấn/du lịch...
  difficulty    INT DEFAULT 1,
  target_goal   TEXT[] DEFAULT '{}',                     -- mục tiêu phù hợp
  focus_phonemes TEXT[] DEFAULT '{}',                    -- âm trọng tâm (BR-CONT-02, FR-WORD-07)
  version       INT NOT NULL DEFAULT 1,
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_content_type_topic ON content_items(type, topic) WHERE is_active;
CREATE INDEX idx_content_focus_phonemes ON content_items USING GIN (focus_phonemes);
CREATE INDEX idx_content_goal ON content_items USING GIN (target_goal);

-- Liên kết content ↔ l1_tag (N:M)
CREATE TABLE content_item_l1_tags (
  content_item_id UUID NOT NULL REFERENCES content_items(id) ON DELETE CASCADE,
  l1_tag_id       UUID NOT NULL REFERENCES l1_error_tags(id) ON DELETE CASCADE,
  PRIMARY KEY (content_item_id, l1_tag_id)
);

-- Minimal Pairs (BR-CONT-03)
CREATE TABLE minimal_pairs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  word_a      TEXT NOT NULL,
  word_b      TEXT NOT NULL,
  phoneme_a   TEXT NOT NULL,
  phoneme_b   TEXT NOT NULL,
  audio_a_us  TEXT, audio_a_uk TEXT,
  audio_b_us  TEXT, audio_b_uk TEXT,
  explain_vi  TEXT,
  l1_tag_id   UUID REFERENCES l1_error_tags(id) ON DELETE SET NULL,
  category    TEXT,                                       -- 'final_sound','th','l_n','v_w','vowel_length' (S15 chips)
  difficulty  INT DEFAULT 1,
  is_free     BOOLEAN NOT NULL DEFAULT FALSE,            -- BR-FREE-05 (cốt lõi luôn free)
  version     INT NOT NULL DEFAULT 1,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_mp_l1 ON minimal_pairs(l1_tag_id) WHERE is_active;

-- Shadowing passage (P2)
CREATE TABLE shadowing_passages (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  title           TEXT NOT NULL,
  source          TEXT NOT NULL DEFAULT 'curated'
                    CHECK (source IN ('curated','daily','news','dialogue')),
  topic           TEXT,
  difficulty      INT DEFAULT 1,
  sentence_count  INT NOT NULL DEFAULT 0,
  version         INT NOT NULL DEFAULT 1,
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE passage_sentences (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  passage_id        UUID NOT NULL REFERENCES shadowing_passages(id) ON DELETE CASCADE,
  order_index       INT NOT NULL,
  text              TEXT NOT NULL,
  ipa               TEXT,
  native_audio_url  TEXT,                                -- TTS cached
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (passage_id, order_index)
);
```

---

## 4. Practice & Scoring (M2/M3) — nguồn của Error Profile

```sql
CREATE TABLE practice_sessions (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  mode           TEXT NOT NULL CHECK (mode IN
                   ('word','sentence','minimal_pair','read_word',
                    'shadowing','exam','conversation')),   -- 'conversation' để sẵn slot
  scoring_level  TEXT NOT NULL DEFAULT 'medium'
                   CHECK (scoring_level IN ('easy','medium','hard')),  -- BR-LEVEL-08
  source         TEXT CHECK (source IN ('recommended','free_choice','daily','onboarding')),
  summary_score  REAL,
  started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at       TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user_time ON practice_sessions(user_id, started_at DESC);
CREATE INDEX idx_sessions_user_mode ON practice_sessions(user_id, mode);

CREATE TABLE practice_item_results (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id       UUID NOT NULL REFERENCES practice_sessions(id) ON DELETE CASCADE,
  content_item_id  UUID REFERENCES content_items(id) ON DELETE SET NULL,  -- null cho MP/exam
  minimal_pair_id  UUID REFERENCES minimal_pairs(id) ON DELETE SET NULL,
  passage_sentence_id UUID REFERENCES passage_sentences(id) ON DELETE SET NULL,
  scoring_level    TEXT NOT NULL CHECK (scoring_level IN ('easy','medium','hard')), -- BR-LEVEL-08

  -- 4 trục (tùy mode; nullable nếu không áp dụng) — BR-SCORE-04
  accuracy         REAL,
  fluency          REAL,
  completeness     REAL,
  prosody          REAL,

  miscue           JSONB DEFAULT '[]'::jsonb,            -- omission/insertion/mispronunciation theo từ
  audio_ref        TEXT,                                 -- bản ghi user (BR-PRIV); hard-delete khi xóa account
  trust_flag       TEXT DEFAULT 'ok'
                     CHECK (trust_flag IN ('ok','flagged','rejected')),  -- §3b.2 sanity-check
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_item_results_session ON practice_item_results(session_id);
CREATE INDEX idx_item_results_content ON practice_item_results(content_item_id);

-- Phoneme-level: LUÔN ghi đầy đủ bất kể Level (BR-SCORE-06, BR-LEVEL-05)
CREATE TABLE phoneme_scores (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  item_result_id    UUID NOT NULL REFERENCES practice_item_results(id) ON DELETE CASCADE,
  expected_phoneme  TEXT NOT NULL,
  said_phoneme      TEXT,                                -- NBest "bạn đã nói âm gì" (FR-WORD-03)
  accuracy          REAL NOT NULL,                       -- 0–100
  word_index        INT,
  phoneme_index     INT,
  is_omission       BOOLEAN NOT NULL DEFAULT FALSE,      -- mất âm cuối (BR-L1-04)
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_phoneme_scores_item ON phoneme_scores(item_result_id);
CREATE INDEX idx_phoneme_scores_expected ON phoneme_scores(expected_phoneme);
```

---

## 5. Error Profile & Coach (M4) — TÀI SẢN LÕI

```sql
CREATE TABLE error_profiles (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id             UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  top_errors          JSONB DEFAULT '[]'::jsonb,         -- denormalize hiển thị nhanh (S07/S19)
  last_recomputed_at  TIMESTAMPTZ,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE phoneme_mastery (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  error_profile_id   UUID NOT NULL REFERENCES error_profiles(id) ON DELETE CASCADE,
  phoneme            TEXT NOT NULL,
  mastery            REAL NOT NULL DEFAULT 0,            -- 0–100 EWMA (BR-COACH-02)
  attempts           INT NOT NULL DEFAULT 0,
  status             TEXT NOT NULL DEFAULT 'weak'
                       CHECK (status IN ('weak','improving','good')),  -- BR-COACH-03
  last_practiced_at  TIMESTAMPTZ,
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (error_profile_id, phoneme)
);
CREATE INDEX idx_phoneme_mastery_status ON phoneme_mastery(error_profile_id, status);

CREATE TABLE pair_mastery (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  error_profile_id   UUID NOT NULL REFERENCES error_profiles(id) ON DELETE CASCADE,
  minimal_pair_id    UUID NOT NULL REFERENCES minimal_pairs(id) ON DELETE CASCADE,
  listen_mastery     REAL NOT NULL DEFAULT 0,            -- nghe phân biệt
  speak_mastery      REAL NOT NULL DEFAULT 0,            -- nói lại
  attempts           INT NOT NULL DEFAULT 0,
  status             TEXT NOT NULL DEFAULT 'weak'
                       CHECK (status IN ('weak','improving','good')),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (error_profile_id, minimal_pair_id)
);

CREATE TABLE skill_mastery (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  error_profile_id   UUID NOT NULL REFERENCES error_profiles(id) ON DELETE CASCADE,
  skill              TEXT NOT NULL CHECK (skill IN
                       ('prosody','fluency','completeness','accuracy','final_consonant')),
  mastery            REAL NOT NULL DEFAULT 0,
  status             TEXT NOT NULL DEFAULT 'weak'
                       CHECK (status IN ('weak','improving','good')),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (error_profile_id, skill)
);
```

---

## 6. Shadowing Progress (M9, P2)

```sql
CREATE TABLE shadowing_progress (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  passage_id             UUID NOT NULL REFERENCES shadowing_passages(id) ON DELETE CASCADE,
  current_sentence_index INT NOT NULL DEFAULT 0,
  sentence_status        JSONB DEFAULT '[]'::jsonb,      -- passed/current/skipped + best_score (BR-SHAD-02)
  attempts_per_sentence  JSONB DEFAULT '{}'::jsonb,      -- đếm để mở "Bỏ qua" (BR-SHAD-04)
  passage_avg_score      REAL,
  completed              BOOLEAN NOT NULL DEFAULT FALSE,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, passage_id)
);
```

---

## 7. Streak & Gamification (M8) + Daily (M10)

```sql
CREATE TABLE streak_records (
  user_id          UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  current_streak   INT NOT NULL DEFAULT 0,
  longest_streak   INT NOT NULL DEFAULT 0,
  last_active_date DATE,                                 -- theo timezone user (BR-STREAK-01)
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE daily_progress (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  date        DATE NOT NULL,                             -- theo timezone user
  items_done  INT NOT NULL DEFAULT 0,
  goal_met    BOOLEAN NOT NULL DEFAULT FALSE,
  xp_earned   INT NOT NULL DEFAULT 0,
  UNIQUE (user_id, date)
);
CREATE INDEX idx_daily_progress_user ON daily_progress(user_id, date DESC);

-- Badge catalog có ĐIỀU KIỆN đạt (S20/S27) — earned/locked + progress
CREATE TABLE badges (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code          TEXT NOT NULL UNIQUE,                    -- streak_7, fix_th, words_100...
  name_vi       TEXT NOT NULL,
  description_vi TEXT,
  icon_url      TEXT,
  criteria_type TEXT NOT NULL CHECK (criteria_type IN
                  ('streak','phoneme_mastered','items_done','pairs_mastered')),
  criteria_value INT NOT NULL,                           -- vd streak=7, items_done=100
  criteria_ref  TEXT,                                    -- vd phoneme '/θ/' nếu cần
  is_active     BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE TABLE user_badges (
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  badge_id    UUID NOT NULL REFERENCES badges(id) ON DELETE CASCADE,
  earned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, badge_id)
);

-- Daily Challenge (M10): trỏ tới passage/content sinh theo ngày
CREATE TABLE daily_challenges (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  date         DATE NOT NULL UNIQUE,
  passage_id   UUID REFERENCES shadowing_passages(id) ON DELETE SET NULL,
  content_item_id UUID REFERENCES content_items(id) ON DELETE SET NULL,
  category     TEXT,                                     -- tag hiển thị (S27)
  banner_url   TEXT,                                     -- ảnh banner (S27)
  moderated    BOOLEAN NOT NULL DEFAULT FALSE,           -- BR-CONT-04
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lịch sử hoàn thành daily challenge theo user/ngày (S27 "Thử thách gần đây")
CREATE TABLE daily_challenge_completions (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  challenge_id UUID NOT NULL REFERENCES daily_challenges(id) ON DELETE CASCADE,
  status       TEXT NOT NULL CHECK (status IN ('completed','missed','in_progress')),
  score        REAL,
  completed_at TIMESTAMPTZ,
  PRIMARY KEY (user_id, challenge_id)
);

-- Snapshot mastery theo thời gian (S19/S28 trend ↑↓, before→after, sparkline)
CREATE TABLE mastery_snapshots (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  snapshot_date   DATE NOT NULL,                         -- chụp theo ngày/tuần
  overall_score   REAL,                                  -- điểm phát âm tổng (S19 "72")
  phoneme_mastery JSONB,                                 -- {phoneme: mastery} tại thời điểm
  skill_mastery   JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, snapshot_date)
);
CREATE INDEX idx_mastery_snapshots_user ON mastery_snapshots(user_id, snapshot_date DESC);
```

---

## 8. Subscription & IAP (M7)

```sql
CREATE TABLE subscriptions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  plan        TEXT NOT NULL DEFAULT 'free'
                CHECK (plan IN ('free','premium','exam_pack')),
  status      TEXT NOT NULL DEFAULT 'active'
                CHECK (status IN ('active','canceled','expired','in_grace')),
  store       TEXT CHECK (store IN ('app_store','google_play')),
  renews_at   TIMESTAMPTZ,                               -- thông báo trước (BR-PAY-04)
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lịch sử giao dịch IAP (verify, chống double-charge BR-PAY-02)
CREATE TABLE iap_transactions (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  store              TEXT NOT NULL CHECK (store IN ('app_store','google_play')),
  product_id         TEXT NOT NULL,
  original_txn_id    TEXT,                               -- để dedupe
  raw_receipt        JSONB,                              -- lưu để audit/verify lại
  status             TEXT NOT NULL CHECK (status IN ('verified','refunded','failed')),
  verified_at        TIMESTAMPTZ,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (store, original_txn_id)
);

-- Đếm freemium quota/ngày (cũng cache ở Redis; bảng để bền & audit)
CREATE TABLE freemium_usage (
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  date        DATE NOT NULL,
  items_used  INT NOT NULL DEFAULT 0,                    -- BR-FREE-01
  PRIMARY KEY (user_id, date)
);

-- Cấu hình gói + giá hiển thị (BR-PAY-01/03, S21) — serve pricing VND minh bạch
CREATE TABLE plan_configs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  plan          TEXT NOT NULL CHECK (plan IN ('premium','exam_pack')),
  product_id_ios TEXT,                                   -- map App Store
  product_id_android TEXT,                               -- map Google Play
  billing_period TEXT CHECK (billing_period IN ('monthly','yearly','one_time')),
  price_vnd     INT,                                     -- giá hiển thị (BR-PAY-03)
  display_name_vi TEXT,
  features_vi   JSONB,                                   -- list tính năng cho card paywall
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

---

## 9. Exam (M13, P3)

```sql
CREATE TABLE exam_prompts (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  exam_type   TEXT NOT NULL CHECK (exam_type IN ('ielts','toeic')),
  part        TEXT NOT NULL,                             -- 'part1','part2','part3'...
  prompt_text TEXT NOT NULL,
  prep_seconds  INT DEFAULT 0,
  speak_seconds INT DEFAULT 0,
  version     INT NOT NULL DEFAULT 1,
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE exam_sessions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  prompt_id   UUID REFERENCES exam_prompts(id) ON DELETE SET NULL,
  exam_type   TEXT NOT NULL CHECK (exam_type IN ('ielts','toeic')),
  audio_ref   TEXT,                                      -- upload để backend chấm Speechace (§4.10)
  band_score  REAL,                                      -- chấm SERVER-SIDE (không nhận từ client)
  criteria    JSONB,                                     -- điểm theo tiêu chí (S33)
  cefr_level  TEXT,                                      -- map band → CEFR ('B2') (S33)
  status      TEXT NOT NULL DEFAULT 'submitted'
                CHECK (status IN ('submitted','scoring','scored','failed')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  scored_at   TIMESTAMPTZ
);
CREATE INDEX idx_exam_sessions_user ON exam_sessions(user_id, created_at DESC);
-- Lưu/chia sẻ exam report = chính exam_sessions đã scored (lịch sử theo user_id).
```

---

## 10. Notification (M12) + Analytics events (doc 10)

```sql
CREATE TABLE notifications (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type        TEXT NOT NULL CHECK (type IN ('streak_reminder','weak_point','daily','renewal')),
  payload     JSONB,
  sent_at     TIMESTAMPTZ,
  read_at     TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications(user_id, created_at DESC);

-- Analytics events (doc 10) — tách khỏi DB chính nếu dùng warehouse; bảng dự phòng
CREATE TABLE analytics_events (
  id          BIGSERIAL PRIMARY KEY,
  user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
  event_name  TEXT NOT NULL,
  properties  JSONB,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_analytics_name_time ON analytics_events(event_name, created_at DESC);

-- Feedback / Bug report (S22 "Trợ giúp & phản hồi", S24 "Báo lỗi")
CREATE TABLE feedback_reports (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
  type        TEXT NOT NULL CHECK (type IN ('feedback','bug','support')),
  message     TEXT NOT NULL,
  context     JSONB,                                     -- app version, device, screen, logs
  status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_feedback_status ON feedback_reports(status, created_at DESC);
```

---

## 10b. App config & Legal (force-update, terms/privacy)

```sql
-- App config (force-update / min-version / feature flags) — S22, S09b "Sắp có"
CREATE TABLE app_configs (
  key         TEXT PRIMARY KEY,                          -- 'min_version_ios','feature_conversation'...
  value       JSONB NOT NULL,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Legal documents versioned (Terms/Privacy) — S02/S23
CREATE TABLE legal_documents (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  doc_type    TEXT NOT NULL CHECK (doc_type IN ('terms','privacy')),
  version     INT NOT NULL,
  content_md  TEXT NOT NULL,
  locale      TEXT NOT NULL DEFAULT 'vi',
  published_at TIMESTAMPTZ,
  UNIQUE (doc_type, version, locale)
);
```

---

## 10c. Minimal Pairs Listen drill (FR-MP-02, S16) — bị sót

```sql
-- Drill nghe-phân-biệt: 1 phiên gồm nhiều câu hỏi listen (hearts/lives, progress)
CREATE TABLE mp_listen_drills (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  total_items   INT NOT NULL,                            -- vd 10 (progress 5/10)
  correct_count INT NOT NULL DEFAULT 0,
  hearts_left   INT NOT NULL DEFAULT 3,                  -- game mechanic (S16)
  status        TEXT NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active','completed','failed')),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at  TIMESTAMPTZ
);

CREATE TABLE mp_listen_answers (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  drill_id      UUID NOT NULL REFERENCES mp_listen_drills(id) ON DELETE CASCADE,
  minimal_pair_id UUID NOT NULL REFERENCES minimal_pairs(id) ON DELETE CASCADE,
  played_word   TEXT NOT NULL,                           -- từ thực sự phát (a/b)
  chosen_word   TEXT,                                    -- user chọn
  is_correct    BOOLEAN,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Kết quả listen → cập nhật pair_mastery.listen_mastery (FR-MP-02)
```

---

## 11. Sơ đồ quan hệ (tóm tắt)

```
users ─1:1─ error_profiles ─1:N─ phoneme_mastery
  │              ├─1:N─ pair_mastery ──N:1── minimal_pairs
  │              └─1:N─ skill_mastery
  ├─1:1─ subscriptions ─1:N─ iap_transactions
  ├─1:1─ streak_records
  ├─1:N─ daily_progress / freemium_usage / user_devices / user_badges
  ├─1:N─ practice_sessions ─1:N─ practice_item_results ─1:N─ phoneme_scores
  ├─1:N─ shadowing_progress ──N:1── shadowing_passages ─1:N─ passage_sentences
  └─1:N─ exam_sessions ──N:1── exam_prompts

content_items ─N:M─ l1_error_tags ─1:N─ fix_guides
minimal_pairs ──N:1── l1_error_tags
daily_challenges → passages / content_items
```

---

## 12. Điểm để "ít phải đổi sau" (checklist)

| Tình huống tương lai | Đã chuẩn bị sẵn |
|---|---|
| **Đổi rule Level** (Dễ/Vừa/Khó) | Server áp Level từ PA thô → đổi logic bỏ đuôi không cần migrate DB cũng không update app. `scoring_level` lưu trên mỗi result (BR-LEVEL-08); `phoneme_scores` luôn đầy đủ |
| Build **Conversation Live** | `mode='conversation'` đã có trong CHECK; `practice_item_results` generic nhận PA |
| Đổi engine (Speechace/self-host) | Score lưu chuẩn hóa 0–100, không gắn vendor; `audio_ref` cho re-score |
| Thêm **L1 tag / fix guide mới** | `version` + `is_active`, không xóa cứng (NFR-MAINT-02) |
| Thêm **skill mới** (vd intonation) | Đổi CHECK 1 dòng, không đổi cấu trúc |
| **Streak freeze** (BR-STREAK-02 sau) | Thêm cột vào `streak_records`, không ảnh hưởng bảng khác |
| **Leaderboard / XP** | `daily_progress.xp_earned` + `user_badges` đã có |
| **Đa accent ngoài US/UK** | `target_accent` CHECK mở rộng dễ; audio_url tách US/UK |
| **GDPR/xóa dữ liệu** | `deleted_at` soft-delete + hard-delete `audio_ref` (BR-PRIV-02) |
| **Partition theo thời gian** | `practice_item_results`/`phoneme_scores`/`analytics_events` có `created_at` để partition khi volume lớn |

---

## 13. Migration & vận hành (khuyến nghị)

- Dùng **golang-migrate** (SQL thuần trong `migrations/`) — schema này dùng **thẳng** làm migration, không sửa schema tay.
- Truy vấn qua **sqlc** (sinh code type-safe từ `store/queries/*.sql`) — chạy `sqlc generate` lại khi đổi query.
- **`phoneme_scores` & `analytics_events` tăng nhanh nhất** → cân nhắc partition theo tháng + retention policy.
- **`top_errors`, `sentence_status`** là `jsonb` denormalized → có thể tái tính từ bảng gốc nếu hỏng (không phải nguồn sự thật).
- Index review định kỳ theo query thực tế (recommendation, history).
- Backup PITR cho PostgreSQL; `raw_receipt` IAP giữ để audit tranh chấp billing.
