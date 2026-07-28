package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/pkg/apperrors"
)

// IssueTokenInput holds the parameters for issuing a speech token.
type IssueTokenInput struct {
	UserID    uuid.UUID
	Engine    string
	SessionID string
	ClientIP  string
}

// SpeechTokenResult is the response from issuing a token.
type SpeechTokenResult struct {
	Token     string    `json:"token"`
	Region    string    `json:"region"`
	Engine    string    `json:"engine"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TokenBrokerService manages the issuance of short-lived speech engine tokens.
// Implements §3c defense layers:
//
//	L1: short TTL tokens bound to session
//	L2: rate-limit per user/IP via Redis
//	L7: cost logging to analytics_events + Redis counter
type TokenBrokerService struct {
	db  *pgxpool.Pool
	rdb *goredis.Client
	cfg *config.Config
}

// NewTokenBrokerService creates a TokenBrokerService.
func NewTokenBrokerService(db *pgxpool.Pool, rdb *goredis.Client, cfg *config.Config) *TokenBrokerService {
	return &TokenBrokerService{db: db, rdb: rdb, cfg: cfg}
}

// IssueToken validates quota/rate-limit and returns a short-lived Azure/Speechace token.
func (s *TokenBrokerService) IssueToken(ctx context.Context, in IssueTokenInput) (*SpeechTokenResult, error) {
	// L2: Rate-limit per user per minute
	userKey := fmt.Sprintf("rl:token:user:%s", in.UserID)
	userCount, err := s.rdb.Incr(ctx, userKey).Result()
	if err != nil {
		return nil, fmt.Errorf("check user rate limit: %w", err)
	}
	if userCount == 1 {
		s.rdb.Expire(ctx, userKey, time.Minute)
	}
	if int(userCount) > s.cfg.RateLimit.TokenPerUserPerMin {
		return nil, apperrors.New(429, "token rate limit exceeded", apperrors.ErrRateLimited)
	}

	// L2: Rate-limit per IP per minute
	ipKey := fmt.Sprintf("rl:token:ip:%s", in.ClientIP)
	ipCount, err := s.rdb.Incr(ctx, ipKey).Result()
	if err != nil {
		return nil, fmt.Errorf("check ip rate limit: %w", err)
	}
	if ipCount == 1 {
		s.rdb.Expire(ctx, ipKey, time.Minute)
	}
	if int(ipCount) > s.cfg.RateLimit.TokenPerIPPerMin {
		return nil, apperrors.New(429, "token rate limit exceeded", apperrors.ErrRateLimited)
	}

	// Check freemium quota — if user is free and has no quota, reject
	if err := s.checkFreemiumQuota(ctx, in.UserID); err != nil {
		return nil, err
	}

	// L5: Check cost circuit breaker
	if err := s.checkCostCircuitBreaker(ctx); err != nil {
		return nil, err
	}

	// Issue token based on engine
	var result *SpeechTokenResult
	switch in.Engine {
	case "azure":
		result, err = s.issueAzureToken(ctx, in)
	case "speechace":
		result, err = s.issueSpeechaceToken(ctx, in)
	default:
		return nil, apperrors.New(400, "unsupported engine", apperrors.ErrBadRequest)
	}
	if err != nil {
		return nil, err
	}

	// L7: Log cost estimate to Redis counter and analytics_events
	s.logTokenIssuance(ctx, in)

	return result, nil
}

func (s *TokenBrokerService) issueAzureToken(ctx context.Context, in IssueTokenInput) (*SpeechTokenResult, error) {
	_ = ctx
	_ = in
	return nil, apperrors.New(
		503, "azure speech token issuance is not configured", apperrors.ErrServiceUnavail,
	)
}

func (s *TokenBrokerService) issueSpeechaceToken(_ context.Context, in IssueTokenInput) (*SpeechTokenResult, error) {
	_ = in
	// The provider credential must never cross the server boundary. Speechace
	// integration must be server-to-server rather than returning its API key.
	return nil, apperrors.New(
		503, "speechace token issuance is not available", apperrors.ErrServiceUnavail,
	)
}

func (s *TokenBrokerService) checkFreemiumQuota(ctx context.Context, userID uuid.UUID) error {
	var plan string
	err := s.db.QueryRow(ctx,
		`SELECT plan FROM subscriptions WHERE user_id = $1 AND status = 'active'`,
		userID).Scan(&plan)
	if err != nil {
		return nil // if no subscription found, allow (will be created on user creation)
	}
	if plan != "free" {
		return nil // premium users bypass quota
	}

	// Check today's usage
	today := time.Now().UTC().Format("2006-01-02")
	redisKey := fmt.Sprintf("quota:free:%s:%s", userID, today)
	count, err := s.rdb.Get(ctx, redisKey).Int()
	if err != nil && err != goredis.Nil {
		return fmt.Errorf("check freemium quota: %w", err)
	}

	if count >= s.cfg.Freemium.DailyLimit {
		return apperrors.New(402, "daily practice limit reached, upgrade to Premium", apperrors.ErrQuotaExceeded)
	}
	return nil
}

func (s *TokenBrokerService) checkCostCircuitBreaker(ctx context.Context) error {
	// L5: check hourly cost counter in Redis
	hour := time.Now().UTC().Format("2006-01-02T15")
	key := fmt.Sprintf("cost:hour:%s", hour)
	cost, err := s.rdb.Get(ctx, key).Float64()
	if err != nil && err != goredis.Nil {
		return nil // don't block on Redis errors
	}
	if cost >= s.cfg.Cost.CircuitBreakerThreshold {
		return apperrors.New(503, "speech service temporarily suspended", apperrors.ErrServiceUnavail)
	}
	return nil
}

func (s *TokenBrokerService) logTokenIssuance(ctx context.Context, in IssueTokenInput) {
	// L7: increment hourly cost counter (~$0.004 per Azure token)
	hour := time.Now().UTC().Format("2006-01-02T15")
	key := fmt.Sprintf("cost:hour:%s", hour)
	s.rdb.IncrByFloat(ctx, key, 0.004)
	s.rdb.Expire(ctx, key, 2*time.Hour)

	// Log analytics event (fire-and-forget)
	go func() {
		s.db.Exec(ctx,
			`INSERT INTO analytics_events (user_id, event_name, properties)
			 VALUES ($1, 'speech_token_issued', $2::jsonb)`,
			in.UserID,
			fmt.Sprintf(`{"engine":"%s","session_id":"%s","est_cost":0.004}`, in.Engine, in.SessionID),
		)
	}()
}
