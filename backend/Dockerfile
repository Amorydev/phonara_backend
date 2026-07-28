# syntax=docker/dockerfile:1

# ── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.26.5-alpine AS builder

WORKDIR /app

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build api binary (static, no CGO)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/api ./cmd/api

# Build worker binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/worker ./cmd/worker

# Build seed binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /out/seed ./cmd/seed

# ── API runtime ───────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS api

COPY --from=builder /out/api /api

EXPOSE 8080
ENTRYPOINT ["/api"]

# ── Worker runtime ────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS worker

COPY --from=builder /out/worker /worker

ENTRYPOINT ["/worker"]

# ── Seed runtime ──────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS seed

COPY --from=builder /out/seed /seed

# KHÔNG có `COPY seeds/ /seeds/`.
#
# Dòng đó từng tồn tại và làm hỏng build trên máy sạch: thư mục `seeds/` rỗng, mà git không
# track thư mục rỗng — nên bản clone mới không có nó và `COPY` thất bại với
# "/seeds: not found". Trên máy phát triển thì build được vì thư mục còn sót lại từ trước.
#
# Không cần nó thật: toàn bộ dữ liệu seed nằm cứng trong cmd/seed/content.go, không đường
# code nào đọc /seeds.

ENTRYPOINT ["/seed"]
