# 14 — Backend Implementation Plan (Go)

> Trạng thái: Draft v0.2 · Liên quan: `12-backend-architecture-full.md`, `13-database-schema.md`
>
> **Phạm vi:** Full P1+P2+P3 (trừ Conversation Live). Plan từng bước, theo 3 tầng build.
> **Đổi stack:** Python → **Go**. Kiến trúc (doc 12) & schema (doc 13) **giữ nguyên** — chỉ stack/cấu trúc code đổi. Milestone/DoD/dependency không đổi.

---

## 0. Stack đã chốt

| Lớp | Lựa chọn |
|---|---|
| Language | **Go 1.23+** |
| Web framework | **Echo** (gọn, middleware tốt) |
| DB access | **sqlc** (sinh code type-safe từ SQL — hợp schema rõ ràng doc 13) + **pgx** driver |
| Migration | **golang-migrate** (SQL thuần từ doc 13) |
| Async jobs | **asynq** (Redis-based, giống Celery: retry/schedule/cron) |
| DB | **PostgreSQL 16** |
| Cache | **Redis 7** |
| Object storage | **S3-compatible** (MinIO local → S3/R2 prod), aws-sdk-go-v2 |
| Deploy | **Docker Compose** (local → VPS/cloud); build 1 binary tĩnh |
| Validation | **go-playground/validator** |
| Auth | **JWT** (golang-jwt) + OAuth verify (google id_token, apple) |
| Test | **testing + testify**; testcontainers cho integration |
| Lint/format | **golangci-lint + gofumpt** |
| Config | **viper** / env (caarlos0/env) |

> **Vì sao Go phù hợp:** backend chủ yếu là API + ingestion + gating + rate-limit (sở trường Go: concurrency, chi phí thấp — khớp NFR-SCALE). **Không bị chặn bởi thiếu Azure Speech SDK** vì client gọi Azure trực tiếp; backend chỉ cấp token (REST) + nhận PA thô (JSON).

---

## 1. Cấu trúc thư mục dự án (Go — layout chuẩn)

```
backend/
├── docker-compose.yml
├── docker-compose.prod.yml
├── Dockerfile                   # multi-stage → 1 binary tĩnh
├── go.mod / go.sum
├── Makefile                     # migrate, sqlc generate, lint, test, run
├── .env.example
├── sqlc.yaml                    # cấu hình sqlc
│
├── cmd/                         # entrypoints (mỗi binary 1 thư mục)
│   ├── api/main.go              # HTTP server (Echo)
│   ├── worker/main.go           # asynq worker
│   └── seed/main.go             # seed content (linguist data)
│
├── migrations/                  # golang-migrate (SQL thuần từ doc 13)
│   ├── 000001_init.up.sql
│   └── 000001_init.down.sql
│
├── internal/                    # code nội bộ (không export ra ngoài module)
│   ├── config/                  # load env (viper)
│   ├── server/                  # Echo setup, middleware, router wiring
│   │   ├── server.go
│   │   └── middleware/          # auth(JWT), ratelimit(§3c L2), logging, recover
│   │
│   ├── handler/                 # HTTP handlers (router mỏng: bind+validate→service)
│   │   ├── auth.go              # M1
│   │   ├── me.go               # M1 (profile, notifications, privacy)
│   │   ├── speech.go           # M2 token broker + ingestion
│   │   ├── session.go          # M3
│   │   ├── content.go          # M5
│   │   ├── coach.go            # M4
│   │   ├── minimalpair.go      # M6 (listen drill + speak)
│   │   ├── shadowing.go        # M9
│   │   ├── progress.go         # M8/M11 (progress, badges, history)
│   │   ├── subscription.go     # M7 (verify/restore/plans)
│   │   ├── daily.go            # M10
│   │   ├── exam.go             # M13
│   │   └── system.go           # health/ready, app-config, legal, feedback
│   │
│   ├── service/                 # business logic (KHÔNG ở handler)
│   │   ├── auth.go
│   │   ├── tokenbroker.go      # §3c: cấp token Azure/Speechace + gating
│   │   ├── scoring.go          # áp Level (Dễ/Vừa/Khó), tính điểm câu
│   │   ├── errorprofile.go     # EWMA, L1 map, top_errors (LÕI)
│   │   ├── recommendation.go   # đề xuất bài
│   │   ├── content.go
│   │   ├── tts.go              # Azure TTS gen + cache
│   │   ├── streak.go           # theo timezone
│   │   ├── freemium.go         # quota/ngày
│   │   ├── iap.go              # verify Apple/Google
│   │   ├── shadowing.go        # ngưỡng 80%, skip
│   │   ├── exam.go             # Speechace server-side
│   │   └── notification.go
│   │
│   ├── store/                   # data layer
│   │   ├── db/                  # sqlc generated (queries.sql.go, models.go)
│   │   ├── queries/             # *.sql cho sqlc (mỗi domain 1 file)
│   │   └── redis/               # redis client (cache, counter, rate-limit)
│   │
│   ├── integration/             # vendor adapters (abstraction NFR-MAINT-01)
│   │   ├── speech/
│   │   │   ├── engine.go        # interface SpeechEngine (PA result chuẩn hóa)
│   │   │   ├── azure.go         # token + parse PA thô
│   │   │   └── speechace.go     # exam scoring
│   │   ├── tts/azure.go
│   │   ├── iap/apple.go
│   │   ├── iap/google.go
│   │   ├── push/fcm.go
│   │   └── storage/s3.go
│   │
│   ├── worker/                  # asynq
│   │   ├── tasks.go             # định nghĩa task types + payload
│   │   └── handlers/
│   │       ├── errorprofile.go  # recompute mastery async
│   │       ├── tts_batch.go     # pre-gen audio
│   │       ├── iap.go           # xử lý webhook
│   │       ├── notification.go  # streak reminder, weak-point
│   │       ├── snapshot.go      # mastery_snapshots định kỳ (cron)
│   │       ├── exam_scoring.go  # gọi Speechace
│   │       └── account.go       # delete account / export data (async)
│   │
│   ├── domain/                  # types/DTO dùng chung (request/response, enums)
│   └── pkg/                     # helper nội bộ (jwt, hash, pagination, errors)
│
└── seeds/                       # dữ liệu seed (json/sql do linguist cung cấp)
    ├── l1_error_tags.json
    ├── minimal_pairs.json
    ├── content_items.json
    └── fix_guides.json
```

**Nguyên tắc (giữ nguyên tinh thần):** handler mỏng (bind + validate → gọi service) → service chứa logic → store (sqlc) cho DB → integration cô lập vendor (đổi Azure/Speechace không sửa service). `cmd/` tách 3 binary: **api**, **worker**, **seed**. Dùng `internal/` để Go chặn import ngoài module.

---

## 2. Lộ trình theo Milestone

> Mỗi milestone = một khối hoàn chỉnh chạy được + test. Theo 3 tầng của doc 12.

### MILESTONE 0 — Khởi tạo nền (foundation)
**Mục tiêu:** project chạy được, có DB, có 1 endpoint health.

- [ ] `go.mod` + deps; `Makefile` (migrate/sqlc/lint/test/run); golangci-lint + gofumpt
- [ ] `docker-compose.yml`: postgres + redis + minio + api + worker
- [ ] `Dockerfile` multi-stage (build → binary tĩnh, image nhỏ)
- [ ] `internal/config` (viper/env) + `internal/pkg` (errors, jwt, hash)
- [ ] `store/db` pgx pool + `store/redis` client; `sqlc.yaml`
- [ ] golang-migrate setup + 1 migration rỗng
- [ ] `cmd/api/main.go`: Echo + middleware (recover, logging, CORS), `internal/server`
- [ ] **Probes tách biệt**: `/health` (liveness) vs `/ready` (check DB + Redis kết nối) — cho deploy (#10)
- [ ] structured logging (slog/zerolog)
- [ ] CI cơ bản (lint + test) — optional
**DoD:** `docker compose up` → `GET /health` 200, `/ready` kiểm tra DB+Redis; migration chạy ok; `go build` ra binary.

---

### TẦNG 1 — Nền tảng (chặn các phần khác)

### MILESTONE 1 — Migration + sqlc toàn bộ schema
**Mục tiêu:** toàn bộ bảng doc 13 thành migration SQL + code truy vấn type-safe (sqlc).

- [ ] Viết `migrations/000001_init.up.sql` (+ down) **trực tiếp từ doc 13** (SQL thuần — không cần dịch ORM)
- [ ] Enum = TEXT + CHECK; `jsonb` cho top_errors/miscue/sentence_status; index (GIN array, composite)
- [ ] Trigger `set_updated_at` (doc 13 §1)
- [ ] Viết `store/queries/*.sql` cho từng domain → `sqlc generate` ra `store/db`
- [ ] `cmd/seed/main.go` khung (chạy được, data rỗng)
**DoD:** `migrate up`/`down` ok; `sqlc generate` không lỗi; quan hệ FK đúng.
> **Lợi thế Go ở đây:** schema doc 13 là SQL thuần → dùng **thẳng** làm migration, không phải dịch sang model ORM. sqlc sinh code khớp 100% schema.

### MILESTONE 2 — Identity & Profile (M1)
- [ ] `service/auth.go`: register/login email (hash bcrypt/argon2), JWT access+refresh
- [ ] Verify Google id_token + Apple id_token (`integration` nếu cần)
- [ ] Guest user (BR-FREE-04)
- [ ] Middleware `auth` (JWT) → context user; biến thể optional (guest)
- [ ] `handler/auth.go`, `handler/me.go`: register/login/refresh/guest, GET/PATCH /me, DELETE /me (soft-delete + hard-delete audio)
- [ ] `me/sync` đa thiết bị (NFR-REL-04)
- [ ] Tự tạo `error_profiles` + `subscriptions(free)` + `streak_records` khi tạo user
- [ ] **Level mapping onboarding** (#9, BR-LEVEL-06): set `default_scoring_level` theo `level` (Sơ→easy, Trung→medium, Khá→hard)
- [ ] **Account deletion task** (#8, BR-PRIV-02): DELETE /me → soft-delete DB + **asynq task** hard-delete tất cả `audio_ref` trên S3 (cascade)
- [ ] **Capture timezone** từ onboarding (case 14) — cần cho streak rollover + reminder
- [ ] **Consent flags** (case 4, BR-PRIV-01/03): `consent_store_recordings`, `consent_improve_product` → chi phối có lưu audio_ref hay không
  - [ ] Endpoint `GET/PATCH /me/privacy` (S23) — đọc/ghi 2 consent toggle
- [ ] **Notification/reminder preferences** (S08/S22): `GET/PATCH /me/notifications` (practice_reminder time+enabled, streak_reminder) + `POST /me/devices` (push token) — đồng bộ đa thiết bị, phục vụ M12
- [ ] **Email auth flow** (case 20): chốt password vs OTP/magic-link (UI S02 không có ô password → cân nhắc OTP)
- [ ] **`mode='assessment'`**: thêm vào CHECK của practice_sessions để onboarding S06 dùng chung session generic (hoặc dùng `source='onboarding'`)
- [ ] Test: auth flow, token refresh, guest→account, deletion xóa audio, consent chi phối lưu audio
**DoD:** đăng ký/đăng nhập/refresh chạy; tạo user kéo theo profile mặc định; xóa account dọn audio; consent tôn trọng.

### MILESTONE 3 — Content Service (M5)
- [ ] `service/content.go` + `store/queries/content.sql` (words/sentences/minimal-pairs/passages/fix-guide)
- [ ] `handler/content.go` endpoints §4.4 + filter (topic/goal/phoneme/l1_tag)
- [ ] Versioned + is_active filter
- [ ] **Seed data thật**: l1_error_tags (taxonomy VN), ~30-50 minimal_pairs, content_items mẫu, fix_guides
- [ ] Test: query + filter + chỉ trả is_active
**DoD:** UI có thể lấy danh sách từ/câu/cặp âm/passage thật.

### MILESTONE 4 — TTS Service (M6)
- [ ] `integration/tts/azure.go`: gọi Azure Neural TTS (SSML), US/UK
- [ ] `service/tts.go`: gen + upload S3 + gắn audio_url vào content
- [ ] asynq task `tts_batch`: pre-gen cho content chưa có audio (BR-CONT-01)
- [ ] Idempotent (đã có audio → skip), cache (~$0)
- [ ] Test: mock Azure, verify upload + url ghi vào DB
**DoD:** chạy batch → content có audio_url_us/uk; phát được.

### MILESTONE 5 — Speech Gateway (M2) + Token broker + Phòng thủ chi phí (§3c)
> **Quan trọng nhất về bảo mật/chi phí.**
- [ ] `integration/speech/engine.go`: interface PA result chuẩn hóa
- [ ] `integration/speech/azure.go`: cấp Azure short-lived token; parse PA thô
- [ ] `service/tokenbroker.go`:
  - [ ] **L1**: token TTL 30–60s, gắn session_id
  - [ ] **L2**: rate-limit cấp token (user/IP/phút) qua Redis; chặn khi hết freemium quota
  - [ ] **L4**: verify App Attest (iOS) / Play Integrity (Android)
  - [ ] **L7**: log chi phí ước tính mỗi token → ghi `analytics_events` (event `speech_token_issued`, props: engine, est_cost) + counter Redis theo giờ cho circuit breaker (KHÔNG bảng riêng)
- [ ] `handler/speech.go`: `POST /speech/token`
- [ ] **L3**: doc + script set Azure budget cap (vận hành, ngoài code)
- [ ] **L5**: anomaly check cơ bản (token/phút bất thường → flag)
- [ ] Test: token gating, rate-limit, từ chối khi hết quota
**DoD:** client lấy được token hợp lệ; vượt quota/rate-limit bị chặn.

### MILESTONE 6 — Practice Session + Scoring (M3) + Ingestion
- [ ] `handler/session.go`: POST /sessions (gating), GET, end, history
- [ ] `service/scoring.go`: **áp Level** (bỏ đuôi -s/-es/-ed theo BR-LEVEL-02..04) trên PA thô → tính accuracy/completeness câu
- [ ] `POST /sessions/{id}/results` ingestion (§4.2): nhận **pa_raw + scoring_level**
  - [ ] **Idempotency key** (#6): client gửi `idempotency_key` → dedup khi retry (BR-QA-03); lưu **trong Redis** (SET NX + TTL, vd 24h) — KHÔNG cần bảng DB
  - [ ] sanity-check (§3b.2 trust boundary) → set trust_flag
  - [ ] **Recording-fail handling** (#4, BR-SCORE-07/QA-01..03): nếu pa_raw báo no_speech/too_short/noise → KHÔNG tạo result, KHÔNG trừ điểm, trả mã lỗi + ghi event `recording_failed`
  - [ ] lưu PracticeItemResult + PhonemeScore (đầy đủ, BR-LEVEL-05)
  - [ ] enqueue asynq task → recompute Error Profile
- [ ] **Batch ingest** (#7, NFR-REL-03): `POST /sessions/{id}/results:batch` nhận nhiều result (offline sync khi có mạng lại)
- [ ] **Minimal Pairs "nói lại"** (#5, FR-MP-03) — `handler/minimalpair.go`: luồng riêng dùng Azure NBest/`sound_most_like` xác định user nói từ A hay B (khác chấm PA thường) → cập nhật pair_mastery.speak
- [ ] **Minimal Pairs "nghe phân biệt"** (case 2, FR-MP-02, S16) — **BỊ SÓT TRONG PLAN GỐC** — `handler/minimalpair.go` (§4.6b): drill listen (mp_listen_drills/answers), phát từ a/b, user chọn, chấm đúng/sai, hearts/lives, progress 5/10 → cập nhật pair_mastery.listen
- [ ] Test: Level easy/medium/hard ra kết quả khác nhau từ cùng PA thô; phoneme luôn lưu đủ; idempotency dedup; recording-fail không tạo result
**DoD:** chấm 1 từ/câu → lưu DB đúng; Level thay đổi đạt/không đạt; retry không trùng; fail không chấm oan.

### MILESTONE 7 — Error Profile Engine + Coach (M4) — LÕI
> Phần đầu tư nhiều nhất.
- [ ] `service/errorprofile.go`:
  - [ ] EWMA cập nhật phoneme_mastery (BR-COACH-02)
  - [ ] Map NBest said≠expected → l1_error_tag → pair_mastery (BR-L1-03)
  - [ ] Omission phụ âm cuối → final_consonant (BR-L1-04)
  - [ ] skill_mastery (prosody/fluency...)
  - [ ] status weak/improving/good (BR-COACH-03)
  - [ ] re-tính top_errors (denormalize) + cache Redis
- [ ] `service/recommendation.go`: rank = severity × frequency × L1_importance × goal_fit; anti-fatigue (BR-COACH-07)
- [ ] **Initial Assessment seeding** (#2, FR-ONB-03, BR-COACH-06): từ kết quả bài đánh giá đầu (S06) → khởi tạo mastery sơ bộ; nếu thiếu dữ liệu → seed theo **prior lỗi phổ biến người Việt**
- [ ] **Historical mastery snapshots** (case 6, FR-COACH-05, S19/S28): asynq cron chụp `mastery_snapshots` định kỳ (ngày/tuần) → tính overall_score + trend ↑↓ per-phoneme + before→after + sparkline
- [ ] asynq task `errorprofile.recompute` (async, không chặn ingestion)
- [ ] `handler/coach.go`: GET /coach/profile (gồm overall_score + trend), /recommendation, /report?period=week|month
- [ ] Test: nhiều result → mastery hội tụ EWMA; đề xuất đúng điểm yếu; assessment seed đúng prior; snapshot tính trend đúng
**DoD:** sau luyện, profile cập nhật; onboarding seed Error Profile (S07); S09 hero + S19 (overall+trend) + S28 (week/month) hiển thị đúng.

---

### TẦNG 2 — Tính năng người dùng

### MILESTONE 8 — Streak & Gamification (M8) + Progress (M11)
- [ ] `service/streak.go`: check-in theo timezone user (BR-STREAK-01), reset, **longest_streak/kỷ lục** (case 13)
- [ ] daily_progress, freemium_usage cập nhật khi luyện
- [ ] **Badge system** (case 7, FR-STREAK-03): catalog `badges` (criteria_type/value), engine kiểm tra điều kiện → earned/locked + progress tới badge kế; `GET /badges`
- [ ] **Replay audio history** (case 5, FR-PROG-02): `GET /sessions/history` trả signed URL S3 cho audio_ref (tôn trọng consent_store_recordings)
- [ ] **Score trend chart** (case 6): serve biểu đồ điểm theo thời gian từ `mastery_snapshots` (snapshot do **M7** tạo; M8 chỉ đọc/serve)
- [ ] `handler/progress.go`: overview, charts, streak/check-in, badges, history (replay)
- [ ] asynq cron: rollover ngày theo timezone
- [ ] Test: streak +1/reset/kỷ lục đúng; goal_met; badge đạt đúng điều kiện; signed URL replay hợp lệ
**DoD:** S20 hiển thị streak/điểm/lịch sử/badge/replay đúng.

### MILESTONE 9 — Subscription & Freemium + IAP (M7)
- [ ] `service/freemium.go`: quota N bài/ngày (BR-FREE-01), Redis counter + bảng bền
- [ ] Gating tích hợp vào POST /sessions + token broker
- [ ] `service/iap.go` + `integration/iap/apple.go`,`google.go`: verify receipt (BR-PAY-02)
- [ ] **Restore Purchase** (case 1, FR-PAY-02) — **BẮT BUỘC store**: `POST /subscription/restore` verify lại entitlement từ store, khôi phục plan
- [ ] **Pricing/plan config** (case 8, BR-PAY-03): seed `plan_configs` (giá VND, product_id) → `GET /subscription/plans` cho paywall S21
- [ ] **Cancel/Manage** (case 10, BR-PAY-04): `GET /subscription` phản ánh trạng thái + deep-link store; xử lý webhook canceled/expired
- [ ] Webhook `/iap/webhook/apple`,`/google` (asynq xử lý)
- [ ] `handler/subscription.go`: GET, verify, restore, plans, quota
- [ ] Test: free hết quota bị chặn; premium mở khóa; verify receipt; restore khôi phục đúng; pricing trả VND
**DoD:** Free gating hoạt động; mua/khôi phục Premium nâng quyền; paywall có giá VND.

### MILESTONE 10 — Shadowing (M9) + Read Word-by-Word (P2)
- [ ] `service/shadowing.go`: ngưỡng 80% (BR-SHAD-02), next/retry, skip sau N lần (BR-SHAD-04)
- [ ] **Server áp Level** từ pa_raw (đã chốt) cho điểm câu
- [ ] `handler/shadowing.go`: progress, sentence-result (nhận pa_raw+level), complete
- [ ] sentence_status / attempts_per_sentence (jsonb)
- [ ] **Read Word-by-Word** (#3, FR-RWW-01..03, S25): mode `read_word` — đọc nối tiếp từng từ, chấm từng từ trước khi ghép (tái dùng `service/scoring.go`); chuyển sang đọc cả câu khi đạt ngưỡng
- [ ] Test: pass auto-advance; <80% retry; skip; tổng kết passage; read-word chấm từng từ
**DoD:** S26 luyện đoạn theo câu chạy đúng; S25 read-word chạy.

### MILESTONE 11 — Daily Challenge (M10) + Notification (M12) + Analytics
- [ ] `daily_challenges`: sinh nội dung ngày (gắn passage/content), moderated flag (BR-CONT-04)
- [ ] `handler/daily.go`: GET /daily/today
- [ ] `service/notification.go` + `integration/push/fcm.go` — mỗi lần gửi: **INSERT `notifications`** (log + read_at tracking) rồi push qua FCM
- [ ] asynq cron: streak reminder, weak-point push (FR-COACH-06, FR-DAILY-04)
- [ ] **Daily Challenge history** (case 12, S27): `daily_challenge_completions` (completed/missed per day) → `GET /daily/history`
- [ ] **Per-type notification preferences** (case 9): tôn trọng `notif_practice_reminder`/`notif_streak_reminder`/`reminder_time` khi gửi push
- [ ] **Analytics ingestion** (#1, doc 10): `POST /v1/events` ghi `analytics_events` (snake_case + properties) HOẶC tích hợp vendor (PostHog/Mixpanel/Amplitude). 15 events doc 10 §4: practice_item_scored, fix_guide_viewed, recording_failed, recommendation_shown...
- [ ] **Viral share tool** (FR-MP-06, S18) — `handler/minimalpair.go` (tái dùng listen drill): sinh mini-test + event `share_result`
- [ ] **Share asset/OG link** (case 18, S18/S28) — `handler/system.go`: generate OG card + dynamic link. **Attribution chỉ qua `analytics_events`** (share_result + utm props), KHÔNG bảng riêng. score→label/tier mapping (case 19) = logic service, không lưu DB
- [ ] Test: daily content + history trả đúng; notification tôn trọng prefs; events ghi đúng schema
**DoD:** S27 Daily Challenge có nội dung + lịch sử; push tôn trọng prefs; analytics events thu thập (nền North Star); share asset hoạt động.

### MILESTONE 11b — Account, Privacy, Support & App Config
> Gom các case account/privacy/support/ops dễ sót (S22/S23/S24/S02/S09b).
- [ ] **Export data** (case 3, BR-PRIV-02, S23): `POST /me/export` → asynq task gom toàn bộ dữ liệu user (profile, history, error profile) → file tải về (GDPR)
- [ ] **Xóa lịch sử luyện tập riêng** (case 3, S23): `DELETE /me/history` (khác xóa account) — xóa sessions/results/audio, giữ account
- [ ] **Feedback/Bug report** (case 11, S22/S24): `POST /feedback` ghi `feedback_reports` (kèm context: version/device/screen/logs)
- [ ] **App config / force-update** (case 16/21, S22/S09b): `GET /app-config` trả min_version + feature flags ("Sắp có" per mode)
- [ ] **Legal docs** (case 23, S02/S23): `GET /legal/{terms|privacy}` versioned từ `legal_documents`
- [ ] **Resume in-progress session** (case 15, S09b): `GET /sessions/in-progress` cho strip "Tiếp tục 2/8"
- [ ] **Fix Guide fallback** (case 17, BR-L1-02, S12): trả simplified guide + cờ rich/simple khi chưa có rich content
- [ ] Test: export đầy đủ; clear-history giữ account; feedback ghi; force-update flag; legal serve
**DoD:** S22/S23/S24 đầy đủ; app config + legal + resume + fix-guide fallback chạy.

---

### TẦNG 3 — Cao cấp

### MILESTONE 12 — Exam Mode (M13, P3) — server-side scoring
- [ ] `integration/speech/speechace.go`: gọi Speechace chấm band
- [ ] `service/exam.go`: nhận audio_ref → asynq `exam_scoring` gọi Speechace → band + criteria
- [ ] **KHÔNG nhận band từ client** (§4.10, §3b.2)
- [ ] **CEFR equivalence** (case 30, S33): map band → CEFR level lưu `exam_sessions.cefr_level`
- [ ] **Save/Share exam report** (case 30): lịch sử exam (`GET /exam/sessions`) + share asset
- [ ] `handler/exam.go`: prompts, sessions, submit, report, history
- [ ] Gating Exam Pack (BR-PAY-06)
- [ ] Test: submit audio → band server-side; gating add-on; CEFR map đúng
**DoD:** S32/S33 luyện thi + báo cáo band + CEFR + lịch sử chạy.

### MILESTONE 13 — Hardening & vận hành
- [ ] Observability: cost dashboard engine (NFR-MAINT-03), metrics, sentry
- [ ] **§3c L5 hoàn chỉnh**: anomaly detection + circuit breaker (cắt cấp token khi chi phí/giờ vượt ngưỡng)
- [ ] **§3c L7**: billing alert đa kênh
- [ ] Rate-limit toàn cục ở gateway
- [ ] Backup PITR Postgres; retention policy phoneme_scores/analytics
- [ ] Partition phoneme_scores/analytics theo tháng (khi cần)
- [ ] Load test endpoint nóng (ingestion, coach)
- [ ] Security review: TLS, at-rest, audio_ref lifecycle (BR-PRIV)
**DoD:** sẵn sàng production; có lưới an toàn chi phí.

---

## 3. Thứ tự phụ thuộc (dependency)

```
M0 ─▶ M1 ─┬─▶ M2 (Identity) ─┬─▶ M5 (Speech Gateway) ─▶ M6 (Scoring) ─▶ M7 (Error Profile) ──┐
          ├─▶ M3 (Content) ──┘                                                                │
          └─▶ M4 (TTS, cần M3)                                                                │
                                                                                              ▼
M8 (Streak/Progress) · M9 (Freemium/IAP) ─▶ M10 (Shadowing/RWW) ─▶ M11 (Daily/Notif/Analytics)
   ─▶ M11b (Account/Privacy/Support/Config) ─▶ M12 (Exam) ─▶ M13 (Hardening)
```

> **Đường găng (critical path):** M1→M2→M5→M6→**M7**. M7 (Error Profile) là moat — ưu tiên hoàn thiện & test kỹ trước khi đổ công Tầng 2/3.

---

## 4. Quy ước kỹ thuật (giảm rework)

| Hạng mục | Quy ước (Go) |
|---|---|
| Handler | Mỏng: bind + validate (validator) → gọi service → trả JSON. KHÔNG logic trong handler |
| Service | Nhận `context.Context` + DTO/domain type; không phụ thuộc Echo `Context` (dễ test) |
| Store | DB qua **sqlc** generated; query phức tạp (Error Profile) viết SQL rõ trong `queries/` |
| Vendor | Chỉ gọi qua `integration/*` **interface** → đổi Azure/Speechace không sửa service |
| Money/gating | LUÔN ở server (freemium, streak, exam band) — không tin client (§3b.2) |
| Async | Việc nặng/không chặn response → **asynq** task; ingestion ghi DB sync, recompute async |
| Errors | Wrap error (`fmt.Errorf("%w")`); map sang HTTP code ở middleware/handler |
| Migration | Chỉ qua **golang-migrate** (SQL từ doc 13); chạy `sqlc generate` lại khi đổi query |
| Secrets | Qua env/secret manager, KHÔNG commit; Azure/Speechace key chỉ ở backend |
| Test | Unit cho service (scoring/EWMA — pure func, dễ test); integration qua testcontainers; mock interface vendor |
| Idempotency | TTS gen, IAP webhook, exam scoring, ingestion phải idempotent |
| Concurrency | Goroutine + context cancel cho I/O song song; cẩn thận race (chạy `-race` trong test) |

---

## 5. Việc cần chuẩn bị song song (không phải code)

- [ ] **Azure account** + Speech resource + budget cap (§3c L3) + Speechace account
- [ ] **Seed content** do linguist: taxonomy L1, minimal pairs, fix-guides (chặn M3)
- [ ] App Store / Google Play product IDs cho IAP (chặn M9)
- [ ] APNs/FCM credentials (chặn M11)
- [ ] App Attest / Play Integrity setup (chặn M5 L4)
- [ ] **Analytics vendor** (PostHog/Mixpanel/Amplitude) hoặc tự host — quyết trước M11 (#1)
- [ ] **Prior lỗi phổ biến người Việt** (data seed cho initial assessment) — linguist cung cấp (chặn M7 #2)

---

## 6. Khuyến nghị triển khai

1. **Hoàn thiện đến M7 trước** (Tầng 1) → có thể demo lõi: luyện từ/câu + Error Profile + đề xuất. Đây là wedge value, validate sớm.
2. Test EWMA & Level logic kỹ (unit) — sai ở đây làm hỏng moat.
3. §3c L1/L2/L3/L7 làm ngay ở M5 (chi phí thấp, chặn rủi ro lớn); L4/L5 hoàn thiện ở M13.
4. Seed content là blocker thật — khởi động sớm song song với M0-M2.

---

## 7. Chi tiết bổ sung (rà soát đối chiếu FR/BR/NFR/metrics)

> Các điểm dễ sót đã được nhúng vào milestone tương ứng. Bảng tra cứu nhanh:

| # | Chi tiết | Milestone | Nguồn |
|---|---|---|---|
| 1 | Analytics events ingestion (15 events) + dashboard nền | M11 | doc 10 §4, North Star |
| 2 | Initial Assessment seed Error Profile (prior lỗi VN) | M7 | FR-ONB-03, BR-COACH-06 |
| 3 | Read Word-by-Word mode | M10 | FR-RWW-01..03, S25 |
| 4 | Recording-fail: không chấm oan, ghi event | M6 | BR-SCORE-07, BR-QA-01..03 |
| 5 | Minimal Pairs "nói lại" (NBest/sound_most_like) | M6 | FR-MP-03 |
| 6 | Idempotency key cho ingestion (retry dedup) | M6 | BR-QA-03 |
| 7 | Batch ingest (offline sync) | M6 | NFR-REL-03 |
| 8 | Account deletion async (hard-delete audio S3) | M2 | BR-PRIV-02 |
| 9 | Level mapping onboarding (Sơ/Trung/Khá → easy/medium/hard) | M2 | BR-LEVEL-06 |
| 10 | Health vs Ready probe tách biệt | M0 | deploy/ops |

### 7b. Rà soát vòng 2 — đối chiếu toàn bộ UI screens (30 case)

> Phát hiện thêm khi quét toàn bộ màn S02–S33. **Quan trọng nhất: case 2 (MP Listen) bị sót dù là Must.**

| Case | Chi tiết | Milestone | Nguồn | Mức |
|---|---|---|---|---|
| 1 | **Restore Purchase** (store bắt buộc) | M9 | FR-PAY-02 | ❗ Bắt buộc |
| 2 | **Minimal Pairs LISTEN drill** (nghe-phân-biệt) — SÓT | M6 | FR-MP-02 | ❗ Bắt buộc |
| 3 | Export data + xóa lịch sử riêng (GDPR) | M11b | BR-PRIV-02 | ❗ Bắt buộc |
| 4 | Consent flags chi phối lưu audio | M2 | BR-PRIV-01/03 | Bắt buộc |
| 5 | Replay audio history (signed URL) | M8 | FR-PROG-02 | Bắt buộc |
| 6 | Historical mastery snapshots (trend, overall, sparkline) | M7 | FR-COACH-05 | Nên có |
| 7 | Badge system (catalog, điều kiện, progress) | M8 | FR-STREAK-03 | Nên có |
| 8 | Pricing/plan config (giá VND) | M9 | BR-PAY-03 | Nên có |
| 9 | Per-type notification preferences | M11 | FR-COACH-06 | Nên có |
| 10 | Cancel/Manage subscription | M9 | BR-PAY-04 | Nên có |
| 11 | Feedback/Bug report endpoint | M11b | support | Nên có |
| 12 | Daily Challenge history | M11 | FR-DAILY | Nên có |
| 13 | Longest streak/kỷ lục | M8 | S20 | Nhỏ |
| 14 | Capture timezone onboarding | M2 | BR-STREAK | Nhỏ |
| 15 | Resume in-progress session | M11b | S09b | Nhỏ |
| 16 | Feature flags "Sắp có" per mode | M11b | S09b | Nhỏ |
| 17 | Fix Guide fallback simplified | M11b | BR-L1-02 | Nhỏ |
| 18 | Share asset/OG image + deep-link | M11 | FR-MP-06 | Nhỏ |
| 19 | Score→label / CEFR mapping | M11/M12 | S18/S33 | Nhỏ |
| 20 | Email password vs OTP (làm rõ) | M2 | S02 | Cần chốt |
| 21 | Force-update/min-version | M11b | ops | Nhỏ |
| 22 | Hearts/lives game (MP listen) | M6 | S16 | client+server |
| 23 | Legal docs serve (Terms/Privacy) | M11b | S02/S23 | Nhỏ |
| 24 | Save/share exam report + CEFR | M12 | S33 | Nhỏ |
| 25 | MP category taxonomy + "mastered X/40" | M3/M8 | S15 | Nhỏ |
| + | Viral share mini-test | M11 | FR-MP-06, S18 | — |

> **Schema bổ sung (doc 13):** consent/notif fields trên users; `mastery_snapshots`; badge criteria; `daily_challenge_completions`; `plan_configs`; `feedback_reports`; `app_configs`; `legal_documents`; `mp_listen_drills`/`mp_listen_answers`; MP `category`; exam `cefr_level`.

### 7c. Rà soát vòng 3 — map endpoint cho 30 màn UI (trừ Conversation)

> Đối chiếu từng màn S01–S33 với endpoint trong doc 12. **27/30 màn đủ.** 3 màn (S08/S22/S23) thiếu 2 nhóm endpoint nhỏ → đã bổ sung vào doc 12 §4.1.

| Nhóm endpoint bổ sung | Màn | Đã thêm |
|---|---|---|
| `GET/PATCH /me/notifications` + `POST /me/devices` (reminder + push token) | S08, S22 | doc 12 §4.1, plan M2/M11 |
| `GET/PATCH /me/privacy` (2 consent toggle) | S23 | doc 12 §4.1, plan M2 |
| `mode='assessment'` (hoặc source=onboarding) cho session generic | S06/S07 | plan M2 |

**Xác nhận màn ĐỦ (không cần thêm):** S01/S05 (client-only), S02 (auth+legal), S03/S04 (PATCH /me), S06/S07 (session generic + coach/profile), S09/S09b (me/progress/quota/recommendation/in-progress/app-config), S10–S14 (content/sessions/token/results/fix-guide), S15–S18 (minimal-pairs + listen drill), S19/S20/S28 (coach/progress/charts/badges/history), S21 (subscription/plans/verify/restore), S24 (feedback), S25/S26 (sessions/shadowing), S27 (daily/today+history), S32/S33 (exam).

> **Kết luận:** sau 3 vòng rà soát (FR/BR/NFR/metrics → 30 case UI → map endpoint 30 màn), API contract phủ **toàn bộ 30 màn** (trừ Conversation S29/S30/S31).
