// Package hash provides password hashing and verification using bcrypt.
package hash

import (
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
