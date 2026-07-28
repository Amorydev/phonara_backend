// Package hash provides password hashing and verification using bcrypt.
package hash

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const defaultCost = 12

// Password hashes a plaintext password using bcrypt.
func Password(plain string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(plain), defaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hashed), nil
}

// CheckPassword compares a plaintext password with a bcrypt hash.
// Returns nil if they match, ErrMismatch otherwise.
func CheckPassword(plain, hashed string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)); err != nil {
		return fmt.Errorf("password mismatch: %w", err)
	}
	return nil
}

// ErrTokenMismatch báo token không khớp hash đã lưu.
var ErrTokenMismatch = errors.New("token mismatch")

// Token băm một token ENTROPY CAO (refresh token, API key) bằng SHA-256.
//
// KHÔNG dùng `Password` cho việc này. Hai lý do, lý do đầu là một sự cố thật:
//
//  1. **bcrypt cắt cứng ở 72 byte và BÁO LỖI nếu dài hơn.** Refresh token là JWT dài
//     200+ byte, nên `bcrypt.GenerateFromPassword` trả
//     "password length exceeds 72 bytes" và MỌI đường tạo phiên đều thất bại — đăng ký,
//     đăng nhập, lẫn đăng nhập khách. Toàn bộ auth ngừng hoạt động.
//
//  2. bcrypt sinh ra để chống dò cạn kiệt trên mật khẩu người đặt, vốn entropy thấp. Cái
//     giá là chậm có chủ đích. Refresh token là chuỗi ngẫu nhiên entropy cao — không có
//     gì để dò, nên trả giá đó là vô ích.
//
// Băm nhanh vẫn đủ mục đích ở đây: nếu CSDL rò rỉ, kẻ tấn công không có token nguyên bản
// để phát lại.
func Token(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// CheckToken so token với hash đã lưu, dùng so sánh thời gian hằng định.
func CheckToken(plain, hashed string) error {
	if subtle.ConstantTimeCompare([]byte(Token(plain)), []byte(hashed)) != 1 {
		return ErrTokenMismatch
	}
	return nil
}
