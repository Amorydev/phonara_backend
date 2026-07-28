#!/usr/bin/env sh
# Kiểm file .env trước khi khởi động, theo đúng luật mà `config.validate()` áp dụng.
#
#   ./scripts/env-check.sh              # kiểm hai file mẫu
#   ./scripts/env-check.sh .env         # kiểm file thật trên server
#
# VÌ SAO CẦN: config sai thì api KHỞI ĐỘNG RỒI CHẾT NGAY, và Docker chỉ hiện
# `Restarting (1)` — không nói biến nào hỏng. Phải đọc log container mới biết.
#
# Đã xảy ra thật khi deploy: `.env.prod.example` viết `JWT_SECRET` trong khi code đọc
# `JWT_ACCESS_SECRET` + `JWT_REFRESH_SECRET`, làm api crash-loop trên VPS.
#
# Các luật dưới đây sao chép từ `internal/config/config.go` hàm `validate()`. Sửa luật ở đó
# thì sửa cả ở đây — `TestEnvCheckMatchesValidate` trong config_test.go giữ hai bên khớp nhau.
#
# POSIX sh, không dùng `mapfile`: macOS còn bash 3.2.

set -eu

cd "$(dirname "$0")/.."

CONFIG_GO="internal/config/config.go"
[ -f "$CONFIG_GO" ] || { echo "không tìm thấy $CONFIG_GO"; exit 1; }

MIN_SECRET_LEN=32
rc=0

# get KEY FILE → in giá trị (chuỗi rỗng nếu không có khoá)
get() {
	sed -n "s/^$1=//p" "$2" 2>/dev/null | head -1
}

fail() { echo "  ❌ $1"; rc=1; }
warn() { echo "  ⚠️  $1"; }

check_file() {
	file=$1
	if [ ! -f "$file" ]; then
		echo "── $file  (không có, bỏ qua)"
		return 0
	fi
	echo "── $file"
	ok=1

	# ── Luật cứng từ validate() ───────────────────────────────────────────────
	acc=$(get JWT_ACCESS_SECRET "$file")
	ref=$(get JWT_REFRESH_SECRET "$file")

	if [ -z "$acc" ]; then
		fail "JWT_ACCESS_SECRET trống hoặc thiếu"; ok=0
	elif [ "${#acc}" -lt "$MIN_SECRET_LEN" ]; then
		fail "JWT_ACCESS_SECRET chỉ ${#acc} ký tự, cần ≥ $MIN_SECRET_LEN"; ok=0
	fi

	if [ -z "$ref" ]; then
		fail "JWT_REFRESH_SECRET trống hoặc thiếu"; ok=0
	elif [ "${#ref}" -lt "$MIN_SECRET_LEN" ]; then
		fail "JWT_REFRESH_SECRET chỉ ${#ref} ký tự, cần ≥ $MIN_SECRET_LEN"; ok=0
	fi

	if [ -n "$acc" ] && [ "$acc" = "$ref" ]; then
		fail "hai khoá JWT giống nhau — phải khác nhau, để rò một khoá không mất cả hai"
		ok=0
	fi

	port=$(get SERVER_PORT "$file")
	if [ -n "$port" ] && { [ "$port" -lt 1 ] 2>/dev/null || [ "$port" -gt 65535 ] 2>/dev/null; }; then
		fail "SERVER_PORT=$port ngoài khoảng 1–65535"; ok=0
	fi

	env_name=$(get APP_ENV "$file")
	cors=$(get CORS_ALLOWED_ORIGINS "$file")
	if [ "$env_name" = "production" ]; then
		if [ -z "$cors" ]; then
			fail "APP_ENV=production nên CORS_ALLOWED_ORIGINS bắt buộc"; ok=0
		elif echo "$cors" | grep -q '\*'; then
			fail "CORS_ALLOWED_ORIGINS chứa dấu sao — production không cho phép"; ok=0
		fi
	fi

	# ── Biến docker-compose khai bắt buộc bằng `${VAR:?}` ─────────────────────
	# Gom danh sách ra file tạm rồi duyệt: `while` sau ống dẫn chạy trong subshell nên
	# gán biến bên trong không thoát ra ngoài được.
	composevars=$(mktemp)
	for f in docker-compose.prod.yml docker-compose.behind-proxy.yml; do
		[ -f "$f" ] && grep -ohE '\$\{[A-Z0-9_]+:\?' "$f" | sed 's/\${//;s/:?//'
	done | sort -u > "$composevars"

	while read -r v; do
		[ -n "$v" ] || continue
		if [ -z "$(get "$v" "$file")" ]; then
			fail "$v thiếu — docker compose sẽ từ chối khởi động"
			ok=0
		fi
	done < "$composevars"
	rm -f "$composevars"

	# ── Cảnh báo mềm ──────────────────────────────────────────────────────────
	[ -n "$(get AZURE_TTS_KEY "$file")" ] || warn "AZURE_TTS_KEY trống — sẽ không sinh được audio mẫu"

	[ "$ok" = 1 ] && echo "  ✓ qua mọi luật bắt buộc"
	return 0
}

if [ $# -gt 0 ]; then
	for f in "$@"; do check_file "$f"; done
else
	check_file .env.example
	check_file .env.prod.example
fi

exit $rc
