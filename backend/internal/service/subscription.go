package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phonara/backend/internal/config"
)

// SubscriptionDTO is the subscription status DTO.
type SubscriptionDTO struct {
	Plan      string     `json:"plan"`
	Status    string     `json:"status"`
	RenewsAt  *time.Time `json:"renews_at,omitempty"`
	Store     *string    `json:"store,omitempty"`
}

// QuotaDTO is the freemium quota DTO.
type QuotaDTO struct {
	Used      int  `json:"items_used"`
	Limit     int  `json:"daily_limit"`
	Remaining int  `json:"remaining"`
	IsPremium bool `json:"is_premium"`
}

// SubscriptionService handles IAP and subscription logic.
type SubscriptionService struct {
	db  *pgxpool.Pool
	cfg *config.Config
}

// NewSubscriptionService creates a SubscriptionService.
func NewSubscriptionService(db *pgxpool.Pool, cfg *config.Config) *SubscriptionService {
	return &SubscriptionService{db: db, cfg: cfg}
}

// Get retrieves the user's current subscription.
func (s *SubscriptionService) Get(ctx context.Context, userID uuid.UUID) (*SubscriptionDTO, error) {
	dto := &SubscriptionDTO{}
	err := s.db.QueryRow(ctx,
		`SELECT plan, status, renews_at, store
		 FROM subscriptions WHERE user_id = $1`,
		userID).Scan(&dto.Plan, &dto.Status, &dto.RenewsAt, &dto.Store)
	if err != nil {
		return &SubscriptionDTO{Plan: "free", Status: "active"}, nil
	}
	return dto, nil
}

// Plans retrieves available pricing plans.
func (s *SubscriptionService) Plans(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx,
		`SELECT plan, product_id_ios, product_id_android, billing_period, price_vnd, display_name_vi, features_vi
		 FROM plan_configs WHERE is_active = TRUE ORDER BY price_vnd ASC`)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()

	plans := make([]map[string]any, 0)
	for rows.Next() {
		var plan, prodIOS, prodAndroid, billing, displayName string
		var priceVND int
		var features any
		if err := rows.Scan(&plan, &prodIOS, &prodAndroid, &billing, &priceVND, &displayName, &features); err != nil {
			return nil, fmt.Errorf("scan plan: %w", err)
		}
		plans = append(plans, map[string]any{
			"plan":              plan,
			"product_id_ios":    prodIOS,
			"product_id_android": prodAndroid,
			"billing_period":    billing,
			"price_vnd":         priceVND,
			"display_name_vi":   displayName,
			"features":          features,
		})
	}
	return plans, nil
}

// Verify verifies an IAP receipt and upgrades subscription.
func (s *SubscriptionService) Verify(ctx context.Context, userID uuid.UUID, store, receipt string) (*SubscriptionDTO, error) {
	// TODO: integrate with Apple/Google IAP verification APIs
	// For now, simulate success
	if receipt == "" {
		return nil, fmt.Errorf("empty receipt")
	}

	_, err := s.db.Exec(ctx,
		`UPDATE subscriptions SET plan = 'premium', status = 'active', store = $2, renews_at = $3
		 WHERE user_id = $1`,
		userID, store, time.Now().AddDate(0, 1, 0))
	if err != nil {
		return nil, fmt.Errorf("update subscription: %w", err)
	}

	return s.Get(ctx, userID)
}

// Restore restores a purchase by re-verifying entitlement from store.
func (s *SubscriptionService) Restore(ctx context.Context, userID uuid.UUID, store, receipt string) (*SubscriptionDTO, error) {
	return s.Verify(ctx, userID, store, receipt)
}

// HandleAppleWebhook processes Apple server notifications (enqueued via asynq in production).
func (s *SubscriptionService) HandleAppleWebhook(ctx context.Context, body map[string]any) error {
	// TODO: parse notificationType and update subscription accordingly
	return nil
}

// HandleGoogleWebhook processes Google RTDN (Real-Time Developer Notifications).
func (s *SubscriptionService) HandleGoogleWebhook(ctx context.Context, body map[string]any) error {
	// TODO: parse message.data and update subscription accordingly
	return nil
}

// GetQuota returns the freemium usage quota for today.
func (s *SubscriptionService) GetQuota(ctx context.Context, userID uuid.UUID) (*QuotaDTO, error) {
	sub, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}

	if sub.Plan != "free" {
		return &QuotaDTO{
			Limit:     -1, // unlimited
			IsPremium: true,
		}, nil
	}

	today := time.Now().UTC().Format("2006-01-02")
	var used int
	s.db.QueryRow(ctx,
		`SELECT COALESCE(items_used, 0) FROM freemium_usage WHERE user_id = $1 AND date = $2`,
		userID, today).Scan(&used)

	limit := s.cfg.Freemium.DailyLimit
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}

	return &QuotaDTO{
		Used:      used,
		Limit:     limit,
		Remaining: remaining,
		IsPremium: false,
	}, nil
}
