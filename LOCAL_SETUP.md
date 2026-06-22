# Hướng dẫn chạy Phonara Backend local

> **OS được hỗ trợ:** macOS (Apple Silicon / Intel) · Linux  
> **Không cần cài PostgreSQL/Redis trực tiếp** — tất cả chạy qua Docker Compose

---

## Yêu cầu

| Công cụ | Phiên bản tối thiểu | Kiểm tra |
|---|---|---|
| **Go** | 1.23+ | `go version` |
| **Docker** | 24+ | `docker --version` |
| **Docker Compose** | v2+ | `docker compose version` |
| **make** | bất kỳ | `make --version` |

> Cài Docker Desktop là đủ (đã bao gồm Compose v2).

---

## Cấu trúc thư mục cần nắm

```
Phonara_Backend/
├── backend/          ← thư mục làm việc chính
│   ├── .env.example
│   ├── .env          ← tạo từ .env.example (không commit)
│   ├── Makefile
│   ├── docker-compose.yml
│   └── migrations/
└── LOCAL_SETUP.md    ← file này
```

---

## Bước 1 — Clone & vào thư mục

```bash
cd /Users/amoryzenith/Project/Phonara_Backend/backend
```

---

## Bước 2 — Tạo file `.env`

```bash
cp .env.example .env
```

File `.env` mặc định đã đủ để chạy local (PostgreSQL + Redis + MinIO đều dùng credentials mặc định). Chỉ cần điền thêm khi dùng tính năng thực tế:

| Biến | Khi nào cần | Lấy ở đâu |
|---|---|---|
| `JWT_ACCESS_SECRET` | **Luôn cần** | Tự sinh: `openssl rand -hex 32` |
| `JWT_REFRESH_SECRET` | **Luôn cần** | Tự sinh: `openssl rand -hex 32` |
| `AZURE_SPEECH_KEY` | Khi test token broker | Azure Portal → Cognitive Services |
| `AZURE_TTS_KEY` | Khi test TTS | Azure Portal |
| `SPEECHACE_API_KEY` | Khi test Exam mode | speechace.com |
| `GOOGLE_CLIENT_ID` | Khi test Google OAuth | Google Cloud Console |
| `APPLE_IAP_SHARED_SECRET` | Khi test IAP | App Store Connect |
| `FCM_SERVER_KEY` | Khi test push notification | Firebase Console |

**Sinh JWT secret nhanh:**
```bash
# Paste kết quả vào .env
openssl rand -hex 32   # cho JWT_ACCESS_SECRET
openssl rand -hex 32   # cho JWT_REFRESH_SECRET
```

---

## Bước 3 — Khởi động infrastructure (DB + Redis + MinIO)

```bash
make docker-up
```

Lệnh này chạy:
- **PostgreSQL 16** → `localhost:5432`
- **Redis 7** → `localhost:6379`
- **MinIO** → API `localhost:9000`, Console `localhost:9001`

Kiểm tra tất cả đã up:
```bash
docker compose ps
```

Output mong đợi:
```
NAME                STATUS
backend-postgres-1  Up (healthy)
backend-redis-1     Up (healthy)
backend-minio-1     Up (healthy)
```

> Chờ tất cả `(healthy)` trước khi sang bước tiếp. Thường mất 10–15 giây.

---

## Bước 4 — Chạy migration

```bash
make migrate-up
```

Lệnh này tạo toàn bộ 34 bảng, indexes, triggers, và seed dữ liệu mặc định vào PostgreSQL.

Kiểm tra migration đã chạy xong:
```bash
make migrate-status
```

---

## Bước 5 — Chạy API server

```bash
make run-api
```

Output mong đợi:
```
time=... level=INFO msg="database connected" host=localhost db=phonara
time=... level=INFO msg="redis connected" addr=localhost:6379
time=... level=INFO msg="starting server" addr=0.0.0.0:8080
```

**Test server đang sống:**
```bash
# Liveness probe
curl http://localhost:8080/health
# {"data":{"status":"ok"}}

# Readiness probe (check DB + Redis)
curl http://localhost:8080/ready
# {"data":{"db":"ok","redis":"ok","status":"ok"}}
```

---

## Bước 6 (tùy chọn) — Chạy Worker

Worker xử lý background jobs (error profile recompute, TTS batch, notifications…).  
Mở **terminal riêng**:

```bash
make run-worker
```

Output mong đợi:
```
{"level":"INFO","msg":"starting worker","concurrency":10}
```

---

## Chạy toàn bộ qua Docker (API + Worker + Migration)

Nếu muốn chạy cả stack trong Docker (không cần Go local):

```bash
# Build images
make docker-build

# Chạy migration trước
docker compose --profile migrate up migrate

# Khởi động api + worker
docker compose up api worker
```

---

## Tóm tắt lệnh hàng ngày

```bash
# Bắt đầu ngày làm việc
make docker-up          # Khởi động DB/Redis/MinIO
make migrate-up         # Đảm bảo schema up-to-date
make run-api            # Chạy API server

# Trong lúc code
make run-worker         # Nếu cần test background jobs
make test               # Chạy test suite
make lint               # Kiểm tra linting
make fmt                # Format code

# Kết thúc ngày
make docker-down        # Tắt tất cả containers (data được giữ trong volumes)
```

---

---

## Swagger UI

Sau khi `make run-api`, truy cập:

**http://localhost:8080/swagger/index.html**

Swagger UI hiển thị toàn bộ **57 endpoints** chia thành 13 tag:

| Tag | Endpoints |
|---|---|
| Auth | Register, Login, Refresh, Guest |
| Me | Profile, Notifications, Privacy, Export, Delete history |
| Speech | Token broker (§3c cost defense layers) |
| Sessions | Create, Get, End, History, InProgress, Ingest, Batch |
| Content | Words, Sentences, MinimalPairs, Passages, FixGuide |
| Coach | Error Profile, Recommendation, Report |
| Shadowing | Progress, SentenceResult, Complete |
| MinimalPairs | Listen drill: Start, Answer, Status |
| Progress | Overview, Charts, Badges, Streak check-in |
| Subscription | Plans, Verify, Restore, Webhooks, Quota |
| Daily | Today, History |
| Exam | Prompts, Sessions, Submit, Report |
| System | Feedback, AppConfig, Legal, Analytics events |

> **Authenticate trong Swagger UI:** click nút **"Authorize" 🔒** → nhập `Bearer <access_token>` (lấy từ `/auth/login` hoặc `/auth/register`) → "Authorize".

### Re-generate docs sau khi sửa annotations

```bash
make swag-gen
```

---

## API Endpoints tham khảo nhanh

Sau khi server chạy trên `http://localhost:8080`:

### Auth (public)
```bash
# Đăng ký email
curl -X POST http://localhost:8080/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"provider":"email","email":"test@example.com","password":"password123"}'

# Đăng nhập
curl -X POST http://localhost:8080/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"provider":"email","email":"test@example.com","password":"password123"}'

# Guest user
curl -X POST http://localhost:8080/v1/auth/guest
```

### Protected (cần Bearer token)
```bash
# Lưu access_token từ response login
TOKEN="<access_token>"

# Lấy profile
curl http://localhost:8080/v1/me \
  -H "Authorization: Bearer $TOKEN"

# Lấy quota freemium
curl http://localhost:8080/v1/freemium/quota \
  -H "Authorization: Bearer $TOKEN"

# Tạo practice session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode":"word","source":"free_choice"}'

# Lấy error profile
curl http://localhost:8080/v1/coach/profile \
  -H "Authorization: Bearer $TOKEN"
```

---

## Troubleshooting

### Lỗi `dial tcp: connection refused` (DB)
```bash
# Kiểm tra postgres có healthy chưa
docker compose ps postgres

# Xem logs postgres
docker compose logs postgres
```

### Lỗi `migrate: no migration` hoặc migration fail
```bash
# Kiểm tra version hiện tại
make migrate-status

# Force về version 0 nếu cần reset hoàn toàn
make migrate-force
# Nhập: 0

# Chạy lại từ đầu
make migrate-up
```

### Lỗi `permission denied` khi chạy MinIO
```bash
# Xóa volume cũ và tạo lại
docker compose down -v
make docker-up
```

### Cổng đã bị dùng
Nếu cổng 5432 / 6379 / 9000 / 8080 đã bị dùng bởi service khác:

Chỉnh trong `docker-compose.yml` phần `ports` (vd `"5433:5432"`) rồi cập nhật `.env` tương ứng.

### Reset hoàn toàn (xóa sạch data)
```bash
make docker-down
docker compose down -v   # xóa volumes
make docker-up
make migrate-up
```

---

## Quản lý MinIO (Object Storage)

Truy cập MinIO Console tại: **http://localhost:9001**

- **Username:** `minioadmin`
- **Password:** `minioadmin`

Tạo bucket `phonara-audio` lần đầu (hoặc dùng AWS CLI):
```bash
# Cài mc (MinIO Client) nếu chưa có
brew install minio/stable/mc

# Thêm alias
mc alias set local http://localhost:9000 minioadmin minioadmin

# Tạo bucket
mc mb local/phonara-audio
```

---

## Quản lý Database (tùy chọn)

Kết nối bằng bất kỳ PostgreSQL client (TablePlus, DBeaver, psql):

| Trường | Giá trị |
|---|---|
| Host | `localhost` |
| Port | `5432` |
| Database | `phonara` |
| Username | `phonara` |
| Password | `phonara_secret` |

Hoặc dùng psql CLI:
```bash
docker exec -it $(docker compose ps -q postgres) \
  psql -U phonara -d phonara
```

---

## Phát triển với live reload (tùy chọn)

Cài [Air](https://github.com/air-verse/air) để tự động reload khi sửa code:

```bash
go install github.com/air-verse/air@latest
air -c .air.toml
```

Tạo `.air.toml`:
```toml
root = "."
tmp_dir = "tmp"

[build]
  cmd = "go build -o ./tmp/api ./cmd/api"
  bin = "./tmp/api"
  include_ext = ["go"]
  exclude_dir = ["tmp", "bin", "migrations", "seeds"]

[log]
  time = true
```
