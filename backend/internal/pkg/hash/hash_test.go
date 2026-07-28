package hash

import (
	"errors"
	"strings"
	"testing"
)

// Refresh token là JWT dài 200+ byte. bcrypt BÁO LỖI khi đầu vào quá 72 byte, và vì
// `issueTokens` băm refresh token nên MỌI đường tạo phiên đều chết: đăng ký, đăng nhập,
// đăng nhập khách. Toàn bộ auth ngừng hoạt động cho tới khi đổi sang SHA-256.
func TestTokenHandlesRealRefreshTokenLength(t *testing.T) {
	t.Parallel()
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		strings.Repeat("a", 180) + ".signaturesignaturesignature"
	if len(jwt) <= 72 {
		t.Fatalf("token mẫu chỉ dài %d byte — không tái hiện được lỗi", len(jwt))
	}

	got := Token(jwt)
	if got == "" {
		t.Fatal("Token trả chuỗi rỗng")
	}
	if err := CheckToken(jwt, got); err != nil {
		t.Fatalf("CheckToken trên chính token vừa băm: %v", err)
	}
}

func TestPasswordStillRejectsOverlongInput(t *testing.T) {
	t.Parallel()
	// Ghi lại giới hạn thật của bcrypt để không ai vô tình dùng `Password` cho token nữa.
	if _, err := Password(strings.Repeat("x", 73)); err == nil {
		t.Fatal("bcrypt nhận đầu vào 73 byte — giả định về giới hạn 72 byte đã sai")
	}
}

func TestCheckTokenRejectsWrongToken(t *testing.T) {
	t.Parallel()
	hashed := Token("refresh-token-abc")
	if err := CheckToken("refresh-token-xyz", hashed); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("err = %v, want ErrTokenMismatch", err)
	}
}

func TestTokenIsDeterministic(t *testing.T) {
	t.Parallel()
	// Khác bcrypt: không có salt. Cần tính chất này để đối chiếu được bằng một phép so
	// sánh chuỗi, thay vì phải gọi hàm xác minh riêng của thư viện.
	if Token("same-input") != Token("same-input") {
		t.Fatal("Token không tất định")
	}
	if Token("a") == Token("b") {
		t.Fatal("hai đầu vào khác nhau cho cùng một hash")
	}
}

func TestPasswordAndTokenAreNotInterchangeable(t *testing.T) {
	t.Parallel()
	// Hash mật khẩu (bcrypt, có salt) không so được bằng CheckToken và ngược lại. Test này
	// biến việc dùng nhầm hàm thành lỗi test thay vì lỗi đăng nhập lúc chạy thật.
	pwHash, err := Password("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckToken("correct-horse", pwHash); err == nil {
		t.Fatal("CheckToken chấp nhận hash bcrypt — hai cơ chế đang bị lẫn")
	}
	if err := CheckPassword("correct-horse", Token("correct-horse")); err == nil {
		t.Fatal("CheckPassword chấp nhận hash SHA-256 — hai cơ chế đang bị lẫn")
	}
}
