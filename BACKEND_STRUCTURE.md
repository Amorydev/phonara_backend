# Phonara Backend — Mô tả cấu trúc & file

> **Stack:** Go 1.23+ · Echo · pgx · asynq · Redis · PostgreSQL 16 · S3-compatible (MinIO)
>
> **Vị trí:** `backend/` (thư mục gốc của toàn bộ Go module)
>
> **Module:** `github.com/phonara/backend`

---

## Tổng quan kiến trúc

```
Request → Echo Router
            → Handler (bind + validate)
                → Service (business logic)
                    → Store / pgxpool (DB queries)
                    → Redis (cache, rate-limit, idempotency)
                    → Integration (Azure, S3, IAP, FCM)
                → Response JSON

Background jobs → asynq Worker
                    → Handler functions (error profile, TTS, exam scoring…)
```

Nguyên tắc:
- **Handler mỏng:** chỉ bind request, validate, gọi service, trả JSON
- **Service chứa toàn bộ business logic**; không phụ thuộc Echo context
- **Store** truy vấn DB qua pgx thuần; không dùng ORM
- **Vendor integration** được cô lập hoàn toàn trong `internal/integration/` (TODO)

---

## Cây thư mục & mô tả từng file

```
backend/
├── .env.example
├── .env
├── .gitignore
├── .golangci.yml
├── Dockerfile
├── Makefile
├── docker-compose.yml
├── go.mod / go.sum
├── sqlc.yaml
│
├── cmd/
│   ├── api/main.go
│   ├── worker/main.go
│   └── seed/main.go
│
├── migrations/
│   ├── 000001_init.up.sql
│   └── 000001_init.down.sql
│
├── internal/
│   ├── config/config.go
│   ├── domain/
│   │   ├── pa.go
│   │   └── response.go
│   ├── handler/
│   │   ├── health.go
│   │   ├── auth.go
│   │   ├── me.go
│   │   ├── speech.go
│   │   ├── session.go
│   │   ├── content.go
│   │   ├── coach.go
│   │   ├── shadowing.go
│   │   ├── minimalpair.go
│   │   ├── progress.go
│   │   ├── subscription.go
│   │   ├── daily.go
│   │   ├── exam.go
│   │   └── system.go
│   ├── service/
│   │   ├── auth.go
│   │   ├── me.go
│   │   ├── tokenbroker.go
│   │   ├── session.go
│   │   ├── content.go
│   │   ├── coach.go
│   │   ├── shadowing.go
│   │   ├── progress.go
│   │   ├── subscription.go
│   │   └── system.go
│   ├── server/
│   │   ├── server.go
│   │   └── middleware/middleware.go
│   ├── store/
│   │   ├── db/pool.go
│   │   ├── queries/users.sql
│   │   └── redis/redis.go
│   ├── worker/tasks.go
│   └── pkg/
│       ├── apperrors/errors.go
│       ├── hash/hash.go
│       ├── jwt/jwt.go
│       └── pagination/pagination.go
│
└── seeds/           ← (trống, chờ linguist cung cấp data)
```

---

## Chi tiết từng file

---

### 📁 Gốc dự án

---

#### `.env.example`
**Mục đích:** Template biến môi trường cho toàn bộ ứng dụng.

**Chức năng:** Liệt kê tất cả biến cần thiết với giá trị mặc định an toàn:
- Server: `APP_ENV`, `SERVER_PORT`, `SERVER_HOST`
- Database: `DB_HOST/PORT/NAME/USER/PASSWORD`, pool settings
- Redis: `REDIS_ADDR`, `REDIS_PASSWORD`, `REDIS_DB`
- JWT: `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET`, TTL
- S3/MinIO: endpoint, bucket, credentials
- Azure Speech / TTS: API key, region, token TTL
- Speechace, Apple IAP, Google IAP, FCM: credentials
- Freemium: `FREEMIUM_DAILY_LIMIT` (số bài/ngày miễn phí)
- Rate-limit token broker: `RATE_LIMIT_TOKEN_PER_USER_PER_MIN`
- Cost circuit breaker: `COST_CIRCUIT_BREAKER_THRESHOLD` (USD/giờ)

> **Không bao giờ commit `.env`**; chỉ commit `.env.example`.

---

#### `.gitignore`
**Mục đích:** Loại trừ file nhạy cảm và build artifact khỏi git.

**Chức năng:** Bỏ qua: binaries (`bin/`), test artifacts (`*.out`), `.env`, `.DS_Store`, IDE folders, vendor directory.

---

#### `.golangci.yml`
**Mục đích:** Cấu hình static analysis / linting cho toàn bộ codebase.

**Chức năng:** Bật các linter:
- `errcheck` — bắt buộc xử lý mọi error
- `gosec` — phát hiện lỗi bảo mật (SQL injection, hardcoded secret…)
- `wrapcheck` — đảm bảo error được wrap đúng cách (`fmt.Errorf("%w", err)`)
- `contextcheck`, `noctx` — bắt buộc truyền context vào DB calls
- `bodyclose` — phát hiện HTTP response body chưa đóng
- `revive`, `gocritic` — code quality

Bỏ qua style/naming (chỉ tập trung bug risk và security).

---

#### `Dockerfile`
**Mục đích:** Multi-stage build tạo 3 binary tĩnh riêng biệt.

**Chức năng:**
| Stage | Output | Base image |
|---|---|---|
| `builder` | Compile cả 3 binary (api, worker, seed) | `golang:1.23-alpine` |
| `api` | Image chạy HTTP server | `gcr.io/distroless/static-debian12` |
| `worker` | Image chạy asynq worker | `gcr.io/distroless/static-debian12` |
| `seed` | Image chạy seed data | `gcr.io/distroless/static-debian12` |

Binary tĩnh `CGO_ENABLED=0`, stripped (`-ldflags="-s -w"`), image distroless ~10MB.

---

#### `Makefile`
**Mục đích:** Automation cho toàn bộ workflow phát triển.

**Các target chính:**

| Target | Mô tả |
|---|---|
| `make build` | Build cả 3 binary vào `bin/` |
| `make run-api` | Chạy API server local (`go run`) |
| `make run-worker` | Chạy asynq worker local |
| `make test` | Chạy test với `-race` |
| `make test-cover` | Test + HTML coverage report |
| `make lint` | Chạy golangci-lint |
| `make fmt` | Format code (gofumpt + goimports) |
| `make migrate-up` | Áp migration lên DB |
| `make migrate-down` | Rollback migration gần nhất |
| `make migrate-create` | Tạo file migration mới (interactive) |
| `make sqlc-gen` | Generate Go code từ SQL queries |
| `make docker-up` | Khởi động toàn bộ stack (Docker Compose) |
| `make tidy` | `go mod tidy` |

---

#### `docker-compose.yml`
**Mục đích:** Orchestrate toàn bộ stack local.

**Services:**

| Service | Image | Port | Mô tả |
|---|---|---|---|
| `postgres` | `postgres:16-alpine` | 5432 | Database chính với healthcheck |
| `redis` | `redis:7-alpine` | 6379 | Cache, rate-limit, asynq queue |
| `minio` | `minio/minio:latest` | 9000/9001 | Object storage local (S3-compatible), console port 9001 |
| `api` | Build từ `Dockerfile` | 8080 | HTTP API server |
| `worker` | Build từ `Dockerfile` | — | Background job worker |
| `migrate` | `migrate/migrate` | — | Chạy migration (profile: `migrate`) |

Services `api` và `worker` chờ healthcheck của `postgres`, `redis`, `minio` xong mới start.

---

#### `go.mod`
**Mục đích:** Định nghĩa module và dependencies.

**Các dependency chính:**
- `github.com/labstack/echo/v4` — HTTP framework
- `github.com/jackc/pgx/v5` — PostgreSQL driver (no ORM)
- `github.com/redis/go-redis/v9` — Redis client
- `github.com/hibiken/asynq` — Async job queue (Redis-backed)
- `github.com/golang-jwt/jwt/v5` — JWT signing/parsing
- `github.com/go-playground/validator/v10` — Request validation
- `github.com/spf13/viper` — Config loading (env + .env file)
- `github.com/google/uuid` — UUID generation
- `github.com/aws/aws-sdk-go-v2` — S3 object storage
- `golang.org/x/crypto` — bcrypt password hashing

---

#### `sqlc.yaml`
**Mục đích:** Cấu hình sqlc để generate Go code type-safe từ SQL queries.

**Chức năng:**
- Input: `internal/store/queries/*.sql` + schema từ `migrations/`
- Output: `internal/store/db/` (models + queries Go code)
- Override type mapping: `uuid` → `github.com/google/uuid.UUID`, `jsonb` → `json.RawMessage`
- Emit JSON tags, interface, pointer for nullable types

---

### 📁 `migrations/`

---

#### `migrations/000001_init.up.sql`
**Mục đích:** Tạo toàn bộ schema PostgreSQL cho P1+P2+P3 (trừ Conversation).

**Chức năng:** Tạo 34 bảng theo thứ tự dependency:

| Nhóm | Bảng |
|---|---|
| Identity & Auth | `users`, `user_devices`, `auth_sessions` |
| Content | `l1_error_tags`, `fix_guides`, `content_items`, `content_item_l1_tags`, `minimal_pairs`, `shadowing_passages`, `passage_sentences` |
| Practice & Scoring | `practice_sessions`, `practice_item_results`, `phoneme_scores` |
| Error Profile | `error_profiles`, `phoneme_mastery`, `pair_mastery`, `skill_mastery` |
| Shadowing | `shadowing_progress` |
| Streak & Gamification | `streak_records`, `daily_progress`, `badges`, `user_badges`, `daily_challenges`, `daily_challenge_completions`, `mastery_snapshots` |
| Subscription & IAP | `subscriptions`, `iap_transactions`, `freemium_usage`, `plan_configs` |
| Exam | `exam_prompts`, `exam_sessions` |
| Notifications & Analytics | `notifications`, `analytics_events`, `feedback_reports` |
| System | `app_configs`, `legal_documents` |
| MP Listen Drill | `mp_listen_drills`, `mp_listen_answers` |

Bao gồm:
- Trigger `set_updated_at()` trên mọi bảng có `updated_at`
- Index cho các query nóng (user+time, phoneme status, analytics)
- CHECK constraints thay vì native ENUM (dễ thêm giá trị mới)
- `JSONB` cho dữ liệu bán cấu trúc (`top_errors`, `miscue`, `sentence_status`)
- Seed mặc định cho `app_configs`

---

#### `migrations/000001_init.down.sql`
**Mục đích:** Rollback toàn bộ migration.

**Chức năng:** DROP theo thứ tự ngược lại để tránh lỗi FK constraint. Xóa sạch mọi bảng, function, extension.

---

### 📁 `cmd/`

---

#### `cmd/api/main.go`
**Mục đích:** Entrypoint của HTTP API server.

**Chức năng:**
1. Load config từ env / `.env` file
2. Thiết lập structured logging: `slog.TextHandler` (dev) hoặc `slog.JSONHandler` (production)
3. Khởi tạo pgxpool connection đến PostgreSQL
4. Khởi tạo go-redis client
5. Tạo JWT Manager với access/refresh secret
6. Khởi tạo Echo server với toàn bộ routes và middleware
7. Đăng ký `go-playground/validator` làm Echo validator
8. Graceful shutdown: lắng nghe `SIGINT`/`SIGTERM`, chờ tối đa 10s để kết thúc in-flight requests

---

#### `cmd/worker/main.go`
**Mục đích:** Entrypoint của background job worker.

**Chức năng:**
1. Load config, kết nối DB
2. Tạo `asynq.Server` với Redis connection và config concurrency
3. Đăng ký queue priorities: `critical:6`, `default:3`, `low:1`
4. Mount tất cả task handlers (từ `internal/worker/tasks.go`)
5. Graceful shutdown khi nhận signal

---

#### `cmd/seed/main.go`
**Mục đích:** Chạy seed dữ liệu vào DB (l1_error_tags, minimal_pairs, content, fix_guides).

**Chức năng:** Skeleton đã có sẵn; cần implement runner cho từng file JSON trong `seeds/` khi linguist cung cấp data.

---

### 📁 `internal/config/`

---

#### `internal/config/config.go`
**Mục đích:** Load và validate toàn bộ application configuration.

**Chức năng:**
- Struct `Config` chứa các sub-struct: `AppConfig`, `ServerConfig`, `DBConfig`, `RedisConfig`, `JWTConfig`, `S3Config`, `AzureConfig`, `IAPConfig`, `FCMConfig`, `FreemiumConfig`, `AsynqConfig`, `RateLimitConfig`, `CostConfig`
- Dùng Viper để đọc từ env vars + file `.env` (tự động, không bắt buộc)
- Có giá trị default cho mọi field
- `DBConfig.DSN()` tạo connection string cho pgxpool
- `DBConfig.MigrationURL()` tạo URL cho golang-migrate
- `ServerConfig.Addr()` trả `host:port`

---

### 📁 `internal/domain/`

---

#### `internal/domain/pa.go`
**Mục đích:** Định nghĩa các kiểu dữ liệu dùng chung cho Pronunciation Assessment (PA).

**Chức năng:**
- `PARawPayload` — payload thô từ Azure Speech SDK gửi lên server
  - `WordScores []WordScore` — điểm từng từ
  - `Phonemes []PhonemeScore` — điểm từng âm (NBest)
  - `Fluency`, `Completeness`, `Prosody` — 3 trục cấp câu
- `WordScore` — `Word`, `Accuracy`, `ErrorType` (Omission/Insertion/Mispronunciation), `WordIndex`
- `PhonemeScore` — `Expected`, `Said` (NBest), `Accuracy`, `WordIndex`, `PhonemeIndex`
- `PhonemeScore.IsTrailingConsonant()` — phát hiện âm cuối (-s/-es/-ed) để bỏ qua khi Level = easy (BR-LEVEL-02..04)

---

#### `internal/domain/response.go`
**Mục đích:** Chuẩn hóa envelope JSON response cho toàn bộ API.

**Chức năng:**
- `Response{Data, Error, Message}` — envelope chuẩn
- `PagedResponse{Data, Meta}` — response có pagination
- `PageMeta{Page, PerPage, TotalItems, TotalPages}`
- `domain.OK(data)` — tạo success response
- `domain.Err(msg)` — tạo error response

---

### 📁 `internal/handler/`

> Mỗi handler là **thin layer**: bind request body → validate → gọi service → trả JSON. Không có business logic.

---

#### `internal/handler/health.go`
**Mục đích:** Probes cho deployment (Kubernetes, Docker healthcheck).

**Endpoints:**
- `GET /health` — **Liveness probe**: luôn trả 200 nếu process còn sống
- `GET /ready` — **Readiness probe**: kiểm tra kết nối DB + Redis; trả 503 nếu degraded

Hai probe **tách biệt** theo yêu cầu (#10) — Kubernetes có thể restart pod nếu ready fail mà không cần restart nếu chỉ health fail.

---

#### `internal/handler/auth.go`
**Mục đích:** Xử lý đăng ký, đăng nhập, refresh token, guest.

**Endpoints:**
- `POST /v1/auth/register` — Đăng ký qua email/Google/Apple
- `POST /v1/auth/login` — Đăng nhập, trả `access_token` + `refresh_token`
- `POST /v1/auth/refresh` — Xoay vòng refresh token (revoke cũ, issue mới)
- `POST /v1/auth/guest` — Tạo guest user (BR-FREE-04)

Request body được validate qua struct tags (`validate:"required,oneof=..."`).

---

#### `internal/handler/me.go`
**Mục đích:** Quản lý profile, cài đặt thông báo, quyền riêng tư, xóa tài khoản.

**Endpoints:**
- `GET /me` — Lấy profile
- `PATCH /me` — Cập nhật goal/level/accent/daily_goal/scoring_level/timezone
- `DELETE /me` — Soft-delete tài khoản + enqueue async xóa audio S3 (#8)
- `POST /me/sync` — Đồng bộ tiến độ đa thiết bị (NFR-REL-04)
- `GET/PATCH /me/notifications` — Đọc/ghi reminder preferences (S08/S22)
- `POST /me/devices` — Đăng ký push token APNs/FCM
- `GET/PATCH /me/privacy` — Đọc/ghi consent flags (BR-PRIV-01/03, S23)
- `POST /me/export` — Enqueue GDPR data export (BR-PRIV-02, M11b)
- `DELETE /me/history` — Xóa lịch sử luyện tập riêng (giữ account)

---

#### `internal/handler/speech.go`
**Mục đích:** Cấp short-lived speech token cho client.

**Endpoints:**
- `POST /v1/speech/token` — Yêu cầu token Azure/Speechace cho 1 session cụ thể

Bắt buộc JWT auth. Delegate toàn bộ logic gating + rate-limit sang `TokenBrokerService`.

---

#### `internal/handler/session.go`
**Mục đích:** Quản lý practice session và ingestion kết quả PA.

**Endpoints:**
- `POST /v1/sessions` — Tạo session mới (gating check)
- `GET /v1/sessions/:id` — Lấy thông tin session
- `POST /v1/sessions/:id/end` — Kết thúc session, tính summary score
- `GET /v1/sessions/history` — Lịch sử session (replay audio, FR-PROG-02)
- `GET /v1/sessions/in-progress` — Session chưa kết thúc (resume, S09b)
- `POST /v1/sessions/:id/results` — **Ingestion**: nhận PA thô từ client, server áp Level, lưu DB
- `POST /v1/sessions/:id/results:batch` — Batch ingest (offline sync, NFR-REL-03)

---

#### `internal/handler/content.go`
**Mục đích:** Lấy nội dung học (từ, câu, minimal pairs, passages, fix guide).

**Endpoints:**
- `GET /v1/content/words?topic=&goal=&phoneme=`
- `GET /v1/content/sentences?topic=&goal=`
- `GET /v1/content/minimal-pairs?l1_tag=&category=`
- `GET /v1/content/passages?source=&difficulty=`
- `GET /v1/content/passages/:id/sentences`
- `GET /v1/content/fix-guide?phoneme=&l1_tag=` — có fallback simplified guide (BR-L1-02)

---

#### `internal/handler/coach.go`
**Mục đích:** Error Profile và gợi ý bài luyện.

**Endpoints:**
- `GET /v1/coach/profile` — Điểm tổng + top_errors + phoneme/skill mastery (S07/S19)
- `GET /v1/coach/recommendation` — Bài đề xuất theo điểm yếu (S09 hero)
- `GET /v1/coach/report?period=week|month` — Báo cáo tiến độ (S28)

---

#### `internal/handler/shadowing.go`
**Mục đích:** Luyện đọc theo đoạn văn (Shadowing, M9).

**Endpoints:**
- `GET /v1/shadowing/:passage_id/progress` — Tiến độ passage hiện tại
- `POST /v1/shadowing/:passage_id/sentence-result` — Nộp kết quả câu (server áp Level, check 80%)
- `POST /v1/shadowing/:passage_id/complete` — Hoàn thành passage, trả tổng kết

---

#### `internal/handler/minimalpair.go`
**Mục đích:** Drill nghe phân biệt âm (Listen drill, FR-MP-02, S16).

**Endpoints:**
- `POST /v1/minimal-pairs/listen/start` — Bắt đầu drill nghe (hearts=3, progress)
- `POST /v1/minimal-pairs/listen/:drill_id/answer` — Nộp câu trả lời, chấm đúng/sai, trừ heart
- `GET /v1/minimal-pairs/listen/:drill_id` — Trạng thái drill hiện tại

---

#### `internal/handler/progress.go`
**Mục đích:** Tiến độ tổng quan, biểu đồ, streak, badges.

**Endpoints:**
- `GET /v1/progress/overview` — Streak + longest_streak + tổng số session (S20)
- `GET /v1/progress/charts?period=week|month` — Score trend từ `mastery_snapshots`
- `GET /v1/badges` — Danh sách badge earned/locked + progress
- `POST /v1/streak/check-in` — Check-in hàng ngày theo timezone user (BR-STREAK-01)

---

#### `internal/handler/subscription.go`
**Mục đích:** Quản lý subscription, IAP, freemium quota.

**Endpoints:**
- `GET /v1/subscription` — Trạng thái plan/status/renews_at
- `GET /v1/subscription/plans` — Danh sách gói + giá VND (BR-PAY-03, S21)
- `POST /v1/subscription/verify` — Verify IAP receipt (BR-PAY-02)
- `POST /v1/subscription/restore` — Restore Purchase bắt buộc của store (FR-PAY-02)
- `POST /v1/iap/webhook/apple` — Apple server notifications
- `POST /v1/iap/webhook/google` — Google RTDN
- `GET /v1/freemium/quota` — Quota còn lại hôm nay (BR-FREE-01)

---

#### `internal/handler/daily.go`
**Mục đích:** Daily Challenge (M10).

**Endpoints:**
- `GET /v1/daily/today` — Nội dung challenge hôm nay + trạng thái user
- `GET /v1/daily/history` — Lịch sử "Thử thách gần đây" (S27)

---

#### `internal/handler/exam.go`
**Mục đích:** Exam Mode (M13, P3) — server-side scoring qua Speechace.

**Endpoints:**
- `GET /v1/exam/prompts?type=&part=` — Danh sách đề thi
- `POST /v1/exam/sessions` — Tạo exam session (gating Exam Pack)
- `POST /v1/exam/sessions/:id/submit` — Upload audio_ref, enqueue async Speechace scoring
- `GET /v1/exam/sessions/:id/report` — Band score + CEFR + criteria (S33)
- `GET /v1/exam/sessions` — Lịch sử exam report (save/share)

> Client **không tự chấm** — chỉ upload audio_ref; server gọi Speechace, lưu band_score server-side (§3b.2 trust boundary).

---

#### `internal/handler/system.go`
**Mục đích:** Các endpoint hệ thống (feedback, app config, legal, analytics).

**Endpoints:**
- `POST /v1/feedback` — Gửi feedback/bug report (S22/S24)
- `GET /v1/app-config` — Force-update flag, feature flags "Sắp có" (S09b/S22)
- `GET /v1/legal/:doc_type` — Terms/Privacy versioned (S02/S23)
- `POST /v1/events` — Ingest analytics event (doc 10, 15 events)

---

### 📁 `internal/service/`

> Chứa toàn bộ business logic. Mỗi service nhận `context.Context` + domain types; **không phụ thuộc Echo**.

---

#### `internal/service/auth.go`
**Mục đích:** Logic xác thực người dùng.

**Chức năng:**
- `Register(email/google/apple)` — Hash password bcrypt, upsert social user, tạo defaults (error_profile + subscription + streak_records) trong 1 transaction
- `Login(email/google/apple)` — Verify credential, issue token pair
- `Refresh` — Validate refresh token + session (không bị revoke), revoke cũ, issue mới
- `CreateGuest` — Tạo user guest với flag `is_guest=TRUE`
- `issueTokens` — Sign JWT access + refresh, lưu `auth_sessions` trong DB
- `createUserDefaults` — Tạo `error_profiles`, `subscriptions(free)`, `streak_records` khi user mới đăng ký
- `verifySocialToken` — Placeholder verify Google/Apple JWT (cần implement JWKS verification)

---

#### `internal/service/me.go`
**Mục đích:** Quản lý profile và cài đặt người dùng.

**Chức năng:**
- `GetProfile` — SELECT user profile (loại trừ soft-deleted)
- `UpdateProfile` — PATCH sử dụng `COALESCE(NULLIF($n,''), field)` để chỉ update field được gửi
- `DeleteAccount` — Soft-delete user; TODO enqueue asynq task hard-delete audio S3
- `SyncData` — Trả payload sync cho đa thiết bị
- `GetNotifPrefs` / `UpdateNotifPrefs` — Đọc/ghi `notif_practice_reminder`, `notif_streak_reminder`, `reminder_time`
- `RegisterDevice` — Upsert `user_devices` (push token), ON CONFLICT update last_seen_at
- `GetPrivacy` / `UpdatePrivacy` — Đọc/ghi `consent_store_recordings`, `consent_improve_product`
- `EnqueueExport` — TODO: asynq task TypeAccountExport (GDPR)
- `DeletePracticeHistory` — Xóa `practice_sessions` của user (giữ account)

---

#### `internal/service/tokenbroker.go`
**Mục đích:** Cấp short-lived speech token với 7 lớp phòng thủ chi phí (§3c).

**Chức năng — theo từng lớp:**

| Lớp | Triển khai |
|---|---|
| **L1** Token TTL 30–60s | `ExpiresAt = now() + SpeechTokenTTL` |
| **L2** Rate-limit user/IP | Redis `INCR` + `EXPIRE 1m` cho `rl:token:user:{id}` và `rl:token:ip:{ip}` |
| **L2** Freemium quota gate | Kiểm tra `subscriptions.plan='free'` + Redis counter quota |
| **L5** Cost circuit breaker | Redis counter `cost:hour:{hour}`, block nếu vượt threshold |
| **L7** Log chi phí | Redis `INCRYBYFLOAT` cost counter + async INSERT `analytics_events` |

Placeholder cho Azure và Speechace token (cần implement HTTP call thật).

---

#### `internal/service/session.go`
**Mục đích:** Quản lý practice session và ingestion PA result.

**Chức năng:**
- `Create` — Tạo session, lấy default_scoring_level nếu không có
- `Get` / `History` / `InProgress` — Truy vấn session
- `End` — Tính `summary_score` = avg(accuracy) từ results, update `ended_at`
- `IngestResult` — **Core ingestion flow:**
  1. Idempotency check: Redis `SET NX 24h` trên `idempotency_key` → skip nếu đã xử lý
  2. Recording-fail check: không phoneme + completeness=0 → trả 422, log event, **không tạo result** (BR-SCORE-07)
  3. `applyLevelScoring` — Server áp Level: easy bỏ trailing consonants, hard tính hết (§3b.0a, BR-LEVEL-02..04)
  4. `sanityCheck` — Flag kết quả nghi ngờ (all 100, quá nhiều phoneme)
  5. INSERT `practice_item_results` + toàn bộ `phoneme_scores` trong 1 transaction (BR-LEVEL-05)
  6. Enqueue async `errorprofile:recompute`
- `IngestBatch` — Xử lý tuần tự từng result, collect lỗi không dừng (offline sync, NFR-REL-03)
- `applyLevelScoring` — Filter `PhonemeScore.IsTrailingConsonant()` cho level easy
- `buildMiscue` — Xây dựng JSONB miscue từ word errors

---

#### `internal/service/content.go`
**Mục đích:** Lấy nội dung học từ DB.

**Chức năng:**
- `ListWords` — Filter theo topic/goal/phoneme với GIN index
- `ListSentences` — Filter theo topic/goal
- `ListMinimalPairs` — JOIN với `l1_error_tags`, filter theo code/category
- `ListPassages` — Filter theo source, trả shadowing passages
- `ListPassageSentences` — Lấy câu theo thứ tự `order_index`
- `GetFixGuide` — Lấy fix guide, fallback `IsSimplified=true` nếu không có (BR-L1-02)

---

#### `internal/service/coach.go`
**Mục đích:** Error Profile và recommendation engine.

**Chức năng:**
- `GetErrorProfile` — JOIN `error_profiles` → `phoneme_mastery` → `skill_mastery`; tính `overall_score` = avg mastery; deserialize `top_errors` JSONB
- `GetRecommendation` — Tìm weak phonemes (mastery thấp nhất), JOIN với `content_items` qua GIN array `focus_phonemes && $1::text[]`; trả ranked list
- `GetReport` — Đọc `mastery_snapshots` theo period (7/30 điểm), trả cho sparkline/chart

> EWMA recompute chạy async trong worker — service chỉ đọc kết quả đã tính.

---

#### `internal/service/shadowing.go`
**Mục đích:** Tiến độ shadowing passage và minimal pair listen drill.

**Chức năng — Shadowing:**
- `GetProgress` — Lấy tiến độ user trên 1 passage; trả initial state nếu chưa bắt đầu
- `SubmitSentenceResult` — Tính điểm câu (reuse `applyLevelScoring`), check ngưỡng 80% (BR-SHAD-02), advance `current_sentence_index` nếu pass, logic skip sau N lần (BR-SHAD-04 TODO)
- `Complete` — Mark passage completed, trả summary

**Chức năng — MinimalPairService (Listen Drill):**
- `StartListenDrill` — Tạo `mp_listen_drills`, pick N cặp âm ngẫu nhiên
- `SubmitAnswer` — Chấm đúng/sai, INSERT `mp_listen_answers`, trừ heart nếu sai, cập nhật `pair_mastery.listen_mastery` (TODO EWMA)
- `GetDrillStatus` — Trả total/correct/hearts/status

---

#### `internal/service/progress.go`
**Mục đích:** Streak, progress overview, badges.

**Chức năng — StreakService:**
- `CheckIn` — UPSERT `streak_records` với logic tăng streak nếu `last_active_date = today - 1`, reset về 1 nếu bị gián đoạn, update `longest_streak = MAX(longest, current)`; tính theo timezone user (BR-STREAK-01)

**Chức năng — ProgressService:**
- `Overview` — Streak hiện tại/dài nhất + tổng số session đã kết thúc
- `Charts` — Đọc `mastery_snapshots` theo period, trả cho biểu đồ score trend

**Chức năng — BadgeService:**
- `List` — LEFT JOIN `badges` với `user_badges` của user; tách thành `earned[]` và `locked[]`

---

#### `internal/service/subscription.go`
**Mục đích:** Subscription, IAP, freemium quota.

**Chức năng:**
- `Get` — Lấy plan/status/renews_at từ `subscriptions`
- `Plans` — Lấy `plan_configs` active (giá VND, product_id cho App Store/Google Play)
- `Verify` — Verify IAP receipt + UPDATE subscription (placeholder, cần Apple/Google API)
- `Restore` — Gọi lại Verify để khôi phục entitlement (FR-PAY-02)
- `HandleAppleWebhook` / `HandleGoogleWebhook` — Placeholder xử lý server notification
- `GetQuota` — Premium: unlimited; Free: đếm `freemium_usage` hôm nay, trả remaining = limit - used

---

#### `internal/service/system.go`
**Mục đích:** Chứa 4 service hỗ trợ hệ thống.

**ExamService:**
- `ListPrompts` — Đề thi active theo type/part
- `Create` — Tạo exam session
- `Submit` — Upload audio_ref, cập nhật status = 'scoring', TODO enqueue asynq TypeExamScoring
- `Report` — Lấy band_score + cefr_level + criteria (chỉ available sau khi scored)
- `ListSessions` — Lịch sử exam của user

**DailyService:**
- `Today` — Lấy daily challenge hôm nay + completion status của user
- `History` — Lịch sử 30 ngày gần nhất (S27)

**SystemService:**
- `SubmitFeedback` — INSERT `feedback_reports` với context JSON
- `GetAppConfig` — SELECT tất cả `app_configs` key-value
- `GetLegalDoc` — Lấy legal document mới nhất theo type (terms/privacy)
- `IngestEvent` — INSERT `analytics_events` (15 events từ doc 10)

---

### 📁 `internal/server/`

---

#### `internal/server/server.go`
**Mục đích:** Setup Echo HTTP server, gắn middleware, đăng ký toàn bộ routes.

**Chức năng:**
- Khởi tạo Echo instance với custom error handler
- Global middleware: `Recover`, `RequestLogger`, `CORS`, `RequestID`
- Route grouping theo resource và auth level:
  - Public: `/health`, `/ready`, `/v1/auth/*`
  - Protected (JWT required): toàn bộ `/v1/me`, `/v1/sessions`, `/v1/coach`, `/v1/speech`, etc.
  - Webhook (no auth): `/v1/iap/webhook/*`
- Wiring handler ← service ← DB/Redis/config

---

#### `internal/server/middleware/middleware.go`
**Mục đích:** Custom middleware cho Echo.

**Chức năng:**
- `ErrorHandler` — Centralized error handler: map `*AppError` / `*echo.HTTPError` / sentinel errors sang HTTP code + JSON response. Log 5xx errors với slog.
- `RequestLogger` — Structured logging mỗi request: method, path, status, duration_ms, remote_ip. Level: Info (2xx), Warn (4xx), Error (5xx).
- `JWT` — Parse Bearer token, extract `user_id` + `is_guest` vào Echo context. Trả 401 nếu thiếu/invalid/expired.
- `JWTOptional` — Như JWT nhưng không reject request nếu không có token (dùng cho public endpoints có personalization).
- `UserIDFromCtx` / `IsGuestFromCtx` — Helper lấy user_id/is_guest từ context.

---

### 📁 `internal/store/`

---

#### `internal/store/db/pool.go`
**Mục đích:** Factory tạo pgxpool connection pool.

**Chức năng:**
- Parse DSN từ `DBConfig` (bao gồm pool settings: max_conns, min_conns, lifetime, idle_time)
- `pgxpool.NewWithConfig` + ping để verify kết nối ngay khi startup
- Trả lỗi có context nếu fail (cho graceful shutdown ở `main.go`)

---

#### `internal/store/redis/redis.go`
**Mục đích:** Factory tạo go-redis client.

**Chức năng:**
- Tạo `goredis.Client` với addr/password/db từ config
- Ping ngay để verify kết nối
- Được dùng cho: rate-limit counters, freemium quota, idempotency keys, cost circuit breaker, asynq queue

---

#### `internal/store/queries/users.sql`
**Mục đích:** SQL queries cho domain `users` — input cho `sqlc generate`.

**Queries có sẵn:**
- `GetUser`, `GetUserByEmail`, `GetUserBySocialID`
- `CreateUser`, `UpdateUserProfile`, `SoftDeleteUser`
- `GetPrivacy`, `UpdatePrivacy`
- `GetNotifPrefs`
- `UpsertDevice`
- `CreateAuthSession`, `GetAuthSession`, `RevokeAuthSession`

> Khi chạy `make sqlc-gen`, sqlc đọc file này và `migrations/*.sql` để generate type-safe Go code vào `internal/store/db/`.

---

### 📁 `internal/worker/`

---

#### `internal/worker/tasks.go`
**Mục đích:** Định nghĩa task types và mount handlers cho asynq worker.

**Task types:**

| Constant | Queue | Mô tả |
|---|---|---|
| `TypeErrorProfileRecompute` | default | Recompute EWMA phoneme/pair/skill mastery sau khi có PA result mới |
| `TypeTTSBatch` | low | Pre-generate Azure TTS audio cho content chưa có audio_url |
| `TypeAccountDelete` | default | Hard-delete audio_ref trên S3 sau soft-delete user (BR-PRIV-02) |
| `TypeAccountExport` | low | Gom dữ liệu GDPR, tạo ZIP, upload S3, notify user |
| `TypeIAPWebhook` | critical | Xử lý IAP webhook async (Apple/Google) |
| `TypeNotification` | default | Gửi push notification qua FCM/APNs |
| `TypeMasterySnapshot` | low | Chụp `mastery_snapshots` hàng ngày/tuần (cron) |
| `TypeExamScoring` | critical | Gọi Speechace API, lưu band_score + cefr_level (server-side, §3b.2) |

Mỗi handler là skeleton với `slog.Info` + TODO comment; cần implement business logic thật.

---

### 📁 `internal/pkg/`

---

#### `internal/pkg/apperrors/errors.go`
**Mục đích:** Sentinel errors và HTTP error mapping chuẩn hóa.

**Chức năng:**
- Sentinel errors: `ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`, `ErrConflict`, `ErrBadRequest`, `ErrQuotaExceeded`, `ErrRateLimited`, `ErrServiceUnavail`, `ErrInternal`
- `AppError{Code, Message, Cause}` — custom error type có HTTP code + user message + internal cause; implement `error` và `Unwrap()`
- `HTTPCode(err)` — map sentinel → HTTP status code bằng `errors.Is`
- `UserMessage(err)` — trả message an toàn cho user (không leak internal detail)

---

#### `internal/pkg/hash/hash.go`
**Mục đích:** Password hashing an toàn.

**Chức năng:**
- `Password(plain)` — Hash bcrypt cost=12; trả hash string
- `CheckPassword(plain, hashed)` — `bcrypt.CompareHashAndPassword`, wrap error

---

#### `internal/pkg/jwt/jwt.go`
**Mục đích:** JWT signing và parsing cho access + refresh tokens.

**Chức năng:**
- `Manager` giữ access/refresh secrets và TTL
- `SignAccess(userID, isGuest)` — HS256, claims: `uid`, `guest`, `exp`, `iat`, `sub`
- `SignRefresh(userID, sessionID)` — HS256, claims: `uid`, `sid` (session bound), `exp`
- `ParseAccess(tokenStr)` — Validate signature + expiry, trả `*Claims`
- `ParseRefresh(tokenStr)` — Validate signature, trả `*RefreshClaims`
- Phân biệt rõ `ErrTokenExpired` để trả lỗi meaningful cho client

---

#### `internal/pkg/pagination/pagination.go`
**Mục đích:** Chuẩn hóa pagination parameters và metadata.

**Chức năng:**
- `New(limit, page)` — Normalize limit (max 100, default 20) và tính offset
- `Page{Limit, Offset}` — Dùng trực tiếp trong SQL `LIMIT $n OFFSET $m`
- `Meta{Page, PerPage, TotalItems, TotalPages}` — Metadata cho API response
- `NewMeta(page, perPage, totalItems)` — Tính `TotalPages = ceil(total/perPage)`

---

## Những phần cần implement tiếp (TODO)

| File | TODO |
|---|---|
| `service/auth.go` | `verifySocialToken`: implement JWKS verification cho Google/Apple |
| `service/tokenbroker.go` | `issueAzureToken`: HTTP call thật đến Azure Cognitive Services |
| `service/tokenbroker.go` | `issueSpeechaceToken`: generate scoped JWT cho Speechace |
| `service/shadowing.go` | Skip logic sau N lần thất bại (BR-SHAD-04) |
| `service/me.go` | `DeleteAccount`: enqueue asynq task hard-delete audio |
| `service/me.go` | `EnqueueExport`: implement GDPR export task |
| `service/subscription.go` | `Verify/Restore`: tích hợp Apple StoreKit + Google Play API |
| `worker/tasks.go` | Implement toàn bộ task handlers (TTS, error profile EWMA, exam scoring…) |
| `internal/integration/` | Implement vendor adapters: Azure Speech, Azure TTS, Speechace, S3, FCM, IAP |
| `internal/store/queries/` | Thêm SQL queries cho các domain còn lại (sessions, content, coach, exam…) |
| `seeds/` | Seed data từ linguist: l1_error_tags, minimal_pairs, content_items, fix_guides |

---

## Khởi động nhanh

```bash
# 1. Copy env
cp .env.example .env
# Điền AZURE_SPEECH_KEY, JWT secrets, etc.

# 2. Start infrastructure
make docker-up

# 3. Run migrations
make migrate-up

# 4. Run API server
make run-api

# 5. Test probes
curl http://localhost:8080/health
curl http://localhost:8080/ready
```
