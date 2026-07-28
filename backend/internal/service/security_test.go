package service

import (
	"context"
	"errors"
	"testing"

	"github.com/phonara/backend/internal/pkg/apperrors"
)

func TestPlaceholderIntegrationsFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	auth := &AuthService{}
	if _, _, _, err := auth.verifySocialToken(ctx, "attacker-controlled", "google"); !errors.Is(
		err, apperrors.ErrServiceUnavail,
	) {
		t.Fatalf("social auth error = %v, want service unavailable", err)
	}

	subscription := &SubscriptionService{}
	if _, err := subscription.Verify(ctx, mustUUID(
		"11111111-2222-3333-4444-555555555555",
	), "app_store", "unverified-receipt"); !errors.Is(err, apperrors.ErrServiceUnavail) {
		t.Fatalf("purchase verification error = %v, want service unavailable", err)
	}

	broker := &TokenBrokerService{}
	if _, err := broker.issueSpeechaceToken(ctx, IssueTokenInput{}); !errors.Is(
		err, apperrors.ErrServiceUnavail,
	) {
		t.Fatalf("speechace error = %v, want service unavailable", err)
	}
}
