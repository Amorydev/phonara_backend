.PHONY: all build test lint fmt migrate-up migrate-down sqlc-gen run-api run-worker seed help

# ── Variables ─────────────────────────────────────────────────────────────────
BINARY_API    = bin/api
BINARY_WORKER = bin/worker
BINARY_SEED   = bin/seed

DB_URL ?= postgres://phonara:phonara_secret@localhost:5432/phonara?sslmode=disable
MIGRATE = migrate -path migrations -database "$(DB_URL)"

# ── Build ─────────────────────────────────────────────────────────────────────
all: build

build:
	@echo "→ Building binaries..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY_API)    ./cmd/api
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY_WORKER) ./cmd/worker
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY_SEED)   ./cmd/seed
	@echo "✓ Build complete"

build-api:
	CGO_ENABLED=0 go build -o $(BINARY_API) ./cmd/api

build-worker:
	CGO_ENABLED=0 go build -o $(BINARY_WORKER) ./cmd/worker

# ── Run ───────────────────────────────────────────────────────────────────────
run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

tts-generate:
	@echo "→ Sinh audio mẫu cho câu chưa có (cần AZURE_TTS_KEY)..."
	go run ./cmd/seed -tts

seed:
	go run ./cmd/seed

# ── Test ─────────────────────────────────────────────────────────────────────
test:
	go test -race -count=1 ./...

test-cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# ── Lint & Format ─────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./...

fmt:
	gofumpt -w .
	goimports -w .

vet:
	go vet ./...

# ── Database ─────────────────────────────────────────────────────────────────
migrate-up:
	@echo "→ Running migrations up..."
	$(MIGRATE) up

migrate-down:
	@echo "→ Rolling back last migration..."
	$(MIGRATE) down 1

migrate-create:
	@read -p "Migration name: " name; \
	migrate create -ext sql -dir migrations -seq $$name

migrate-status:
	$(MIGRATE) version

migrate-force:
	@read -p "Version to force: " ver; \
	$(MIGRATE) force $$ver

# ── Swagger ──────────────────────────────────────────────────────────────────
SWAG = $(shell go env GOPATH)/bin/swag

swag-gen:
	@echo "→ Generating Swagger docs..."
	$(SWAG) init \
		--generalInfo docs/doc.go \
		--dir . \
		--output docs \
		--outputTypes go,json,yaml \
		--parseInternal \
		--parseDepth 3
	@echo "✓ Swagger docs generated → docs/"

swag-install:
	go install github.com/swaggo/swag/cmd/swag@v1.16.6

# ── sqlc ─────────────────────────────────────────────────────────────────────
sqlc-gen:
	sqlc generate

# ── Docker ───────────────────────────────────────────────────────────────────
docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f api worker

docker-build:
	docker compose build

# ── Tidy ─────────────────────────────────────────────────────────────────────
tidy:
	go mod tidy

# ── Help ─────────────────────────────────────────────────────────────────────
help:
	@echo "Available targets:"
	@echo "  build          Build all binaries"
	@echo "  run-api        Run API server locally"
	@echo "  run-worker     Run asynq worker locally"
	@echo "  seed           Run seed runner"
	@echo "  test           Run tests with race detection"
	@echo "  test-cover     Run tests with coverage report"
	@echo "  lint           Run golangci-lint"
	@echo "  fmt            Format code (gofumpt + goimports)"
	@echo "  migrate-up     Run all pending migrations"
	@echo "  migrate-down   Roll back the last migration"
	@echo "  migrate-create Create a new migration file"
	@echo "  swag-gen       Generate Swagger UI docs từ annotations"
	@echo "  swag-install   Cài swag CLI tool"
	@echo "  sqlc-gen       Generate Go code từ SQL queries"
	@echo "  docker-up      Start all services via Docker Compose"
	@echo "  docker-down    Stop all services"
	@echo "  tidy           Tidy go.mod"

.PHONY: env-check
## env-check: đối chiếu .env với luật trong config.validate() — chạy TRƯỚC khi khởi động
env-check:
	@./scripts/env-check.sh $(FILE)
