package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Header của `scripts/env-check.sh` hứa:
//
//	"Các luật dưới đây sao chép từ internal/config/config.go hàm validate(). Sửa luật ở đó
//	 thì sửa cả ở đây — TestEnvCheckMatchesValidate trong config_test.go giữ hai bên khớp nhau."
//
// Test đó KHÔNG TỒN TẠI cho tới khi file này ra đời. Hệ quả: `validate()` đã có ba luật
// `PRONUNCIATION_ENGINE_*` mà script không hề kiểm, và không có gì phát hiện ra.
//
// Vì sao điều đó quan trọng: config sai làm api KHỞI ĐỘNG RỒI CHẾT NGAY, và Docker chỉ
// hiện `Restarting (1)` — không nói biến nào hỏng. `make env-check` tồn tại để bắt lỗi đó
// TRƯỚC khi khởi động; mỗi luật thiếu trong script là một lần crash-loop phải đi đọc log
// container mới hiểu.
func TestEnvCheckMatchesValidate(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatalf("đọc config.go: %v", err)
	}
	script, err := os.ReadFile("../../scripts/env-check.sh")
	if err != nil {
		t.Fatalf("đọc env-check.sh: %v", err)
	}

	vars := varsNamedInValidate(t, string(source))
	if len(vars) == 0 {
		t.Fatal("không trích được tên biến nào từ validate() — regex đã lạc hậu so với code")
	}

	for _, name := range vars {
		if !strings.Contains(string(script), name) {
			t.Errorf(
				"validate() chặn %s nhưng scripts/env-check.sh không kiểm nó.\n"+
					"Thêm luật tương ứng vào script, hoặc `make env-check` sẽ báo xanh trong khi "+
					"api crash-loop trên server.",
				name,
			)
		}
	}
}

// varsNamedInValidate → tên các biến môi trường xuất hiện ở ĐẦU thông điệp lỗi của
// `Validate()`.
//
// Quy ước neo vào: mỗi lỗi cấu hình mở đầu bằng đúng tên biến, ví dụ
// `"PRONUNCIATION_ENGINE_TIMEOUT must be positive"`. Nhờ vậy trích được bằng regex thay vì
// phải giữ một danh sách tay — danh sách tay chính là thứ vừa mục ruỗng ở script.
func varsNamedInValidate(t *testing.T, source string) []string {
	t.Helper()

	start := strings.Index(source, "func (c *Config) Validate()")
	if start == -1 {
		t.Fatal("không thấy hàm Validate() trong config.go — đã đổi tên?")
	}
	body := source[start:]

	// Chuỗi lỗi mở đầu bằng TÊN_BIẾN in hoa. Tối thiểu 2 đoạn nối bằng `_` để không bắt
	// nhầm những chuỗi như "JWT TTL values must be positive" (không nêu tên biến cụ thể).
	pattern := regexp.MustCompile(`"([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)[ "]`)

	seen := map[string]bool{}
	var out []string
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		name := match[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// COMPOSE_FILE đi theo hướng NGƯỢC LẠI: nó không nằm trong `validate()` (không phải biến
// của Go config) nhưng phải có trong script, vì thiếu nó trên production làm
// `docker compose up -d` trần rơi về bản dev và phơi Postgres/Redis/MinIO ra internet.
//
// Test này chốt lại rằng lớp bảo vệ đó không bị ai dọn đi vì "không thấy trong config.go".
func TestEnvCheckGuardsComposeFile(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile("../../scripts/env-check.sh")
	if err != nil {
		t.Fatalf("đọc env-check.sh: %v", err)
	}
	text := string(script)

	if !strings.Contains(text, "COMPOSE_FILE") {
		t.Fatal("env-check.sh không kiểm COMPOSE_FILE — xem sự cố mô tả trong chính script đó")
	}
	for _, want := range []string{"docker-compose.prod.yml", "docker-compose.behind-proxy.yml"} {
		if !strings.Contains(text, want) {
			t.Errorf("env-check.sh không nhận %s là giá trị COMPOSE_FILE hợp lệ", want)
		}
	}
}
