package jwtutil

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestManagerRoundTrip(t *testing.T) {
	t.Parallel()
	manager := New(
		"access-secret-that-is-long-enough-123",
		"refresh-secret-that-is-long-enough-456",
		time.Minute,
		time.Hour,
	)
	userID := uuid.New()

	access, err := manager.SignAccess(userID, false)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.ParseAccess(access)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != userID {
		t.Fatalf("user id = %s, want %s", claims.UserID, userID)
	}

	refresh, err := manager.SignRefresh(userID, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ParseRefresh(refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ParseAccess(refresh); err == nil {
		t.Fatal("refresh token must not be accepted as an access token")
	}
}

func TestParseAccessRejectsDifferentHMACAlgorithm(t *testing.T) {
	t.Parallel()
	secret := "access-secret-that-is-long-enough-123"
	manager := New(secret, "refresh-secret-that-is-long-enough-456", time.Minute, time.Hour)
	claims := Claims{
		UserID: uuid.New(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{accessAudience},
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS384, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ParseAccess(token); err == nil {
		t.Fatal("HS384 token must be rejected")
	}
}
