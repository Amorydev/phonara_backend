// Package service contains all business logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/pkg/apperrors"
	"github.com/phonara/backend/internal/pkg/hash"
	jwtutil "github.com/phonara/backend/internal/pkg/jwt"
)

// AuthTokens holds the token pair returned after authentication.
type AuthTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	UserID       uuid.UUID
}

// RegisterInput holds registration parameters.
type RegisterInput struct {
	Provider    string
	Email       string
	Password    string
	IDToken     string
	DisplayName string
}

// LoginInput holds login parameters.
type LoginInput struct {
	Provider string
	Email    string
	Password string
	IDToken  string
}

// AuthService handles authentication business logic.
type AuthService struct {
	db     *pgxpool.Pool
	rdb    *goredis.Client
	jwtMgr *jwtutil.Manager
}

// NewAuthService creates an AuthService.
func NewAuthService(db *pgxpool.Pool, rdb *goredis.Client, jwtMgr *jwtutil.Manager) *AuthService {
	return &AuthService{db: db, rdb: rdb, jwtMgr: jwtMgr}
}

// Register creates a new user account and returns auth tokens.
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (*AuthTokens, error) {
	switch in.Provider {
	case "email":
		return s.registerEmail(ctx, in)
	case "google":
		return s.registerSocial(ctx, in, "google")
	case "apple":
		return s.registerSocial(ctx, in, "apple")
	default:
		return nil, apperrors.New(400, "unsupported provider", apperrors.ErrBadRequest)
	}
}

func (s *AuthService) registerEmail(ctx context.Context, in RegisterInput) (*AuthTokens, error) {
	// Check for duplicate email
	var existing uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1 AND deleted_at IS NULL`,
		in.Email).Scan(&existing)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check existing user: %w", err)
	}
	if err == nil {
		return nil, apperrors.New(409, "email already registered", apperrors.ErrConflict)
	}

	pwHash, err := hash.Password(in.Password)
	if err != nil {
		return nil, err
	}

	userID := uuid.New()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx,
		`INSERT INTO users (id, auth_provider, email, display_name, password_hash)
		 VALUES ($1, 'email', $2, $3, $4)`,
		userID, in.Email, in.DisplayName, pwHash)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	if err := s.createUserDefaults(ctx, tx, userID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit register: %w", err)
	}

	return s.issueTokens(ctx, userID, false)
}

func (s *AuthService) registerSocial(ctx context.Context, in RegisterInput, provider string) (*AuthTokens, error) {
	externalID, email, displayName, err := s.verifySocialToken(ctx, in.IDToken, provider)
	if err != nil {
		return nil, err
	}
	if in.DisplayName != "" {
		displayName = in.DisplayName
	}

	// Upsert: find or create
	var userID uuid.UUID
	err = s.db.QueryRow(ctx,
		`SELECT id FROM users WHERE auth_provider = $1 AND external_auth_id = $2 AND deleted_at IS NULL`,
		provider, externalID).Scan(&userID)

	if errors.Is(err, pgx.ErrNoRows) {
		// New user
		userID = uuid.New()
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck

		_, err = tx.Exec(ctx,
			`INSERT INTO users (id, auth_provider, email, external_auth_id, display_name)
			 VALUES ($1, $2, $3, $4, $5)`,
			userID, provider, email, externalID, displayName)
		if err != nil {
			return nil, fmt.Errorf("insert social user: %w", err)
		}

		if err := s.createUserDefaults(ctx, tx, userID); err != nil {
			return nil, err
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit register social: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("find social user: %w", err)
	}

	return s.issueTokens(ctx, userID, false)
}

// Login authenticates an existing user.
func (s *AuthService) Login(ctx context.Context, in LoginInput) (*AuthTokens, error) {
	switch in.Provider {
	case "email":
		return s.loginEmail(ctx, in)
	case "google":
		return s.loginSocial(ctx, in, "google")
	case "apple":
		return s.loginSocial(ctx, in, "apple")
	default:
		return nil, apperrors.New(400, "unsupported provider", apperrors.ErrBadRequest)
	}
}

func (s *AuthService) loginEmail(ctx context.Context, in LoginInput) (*AuthTokens, error) {
	var (
		userID uuid.UUID
		pwHash string
	)
	err := s.db.QueryRow(ctx,
		`SELECT id, COALESCE(password_hash, '') FROM users
		 WHERE email = $1 AND auth_provider = 'email' AND deleted_at IS NULL`,
		in.Email).Scan(&userID, &pwHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.New(401, "invalid credentials", apperrors.ErrUnauthorized)
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}

	if err := hash.CheckPassword(in.Password, pwHash); err != nil {
		return nil, apperrors.New(401, "invalid credentials", apperrors.ErrUnauthorized)
	}

	return s.issueTokens(ctx, userID, false)
}

func (s *AuthService) loginSocial(ctx context.Context, in LoginInput, provider string) (*AuthTokens, error) {
	externalID, _, _, err := s.verifySocialToken(ctx, in.IDToken, provider)
	if err != nil {
		return nil, err
	}

	var userID uuid.UUID
	err = s.db.QueryRow(ctx,
		`SELECT id FROM users WHERE auth_provider = $1 AND external_auth_id = $2 AND deleted_at IS NULL`,
		provider, externalID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Auto-register on first login
		return s.registerSocial(ctx, RegisterInput{Provider: provider, IDToken: in.IDToken}, provider)
	}
	if err != nil {
		return nil, fmt.Errorf("find social user: %w", err)
	}

	return s.issueTokens(ctx, userID, false)
}

// Refresh rotates a refresh token and returns new token pair.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*AuthTokens, error) {
	claims, err := s.jwtMgr.ParseRefresh(refreshToken)
	if err != nil {
		return nil, apperrors.New(401, "invalid refresh token", apperrors.ErrUnauthorized)
	}

	// Atomically consume the session. A SELECT followed by UPDATE lets two
	// concurrent refresh requests both pass and mint two valid token pairs.
	var consumed uuid.UUID
	var storedHash string
	err = s.db.QueryRow(ctx,
		`UPDATE auth_sessions
		    SET revoked_at = now()
		   FROM users
		  WHERE auth_sessions.id = $1
		    AND auth_sessions.user_id = $2
		    AND users.id = auth_sessions.user_id
		    AND users.deleted_at IS NULL
		    AND revoked_at IS NULL AND expires_at > now()
		RETURNING auth_sessions.id, auth_sessions.refresh_hash`,
		claims.SessionID, claims.UserID).Scan(&consumed, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.New(401, "session expired or revoked", apperrors.ErrUnauthorized)
	}
	if err != nil {
		return nil, fmt.Errorf("consume refresh session: %w", err)
	}

	// Đối chiếu token với hash đã lưu.
	//
	// Trước đây `refresh_hash` được GHI nhưng không bao giờ ĐỌC, nên cột này chỉ tốn chỗ.
	// Chữ ký JWT cộng trạng thái phiên đã chặn phần lớn tấn công, nhưng thiếu bước này thì
	// một token cũ ĐÚNG chữ ký, ĐÚNG session_id vẫn đi lọt — ví dụ token bị rò qua log hay
	// bản sao lưu, trong khi phiên chưa hết hạn.
	//
	// Kiểm SAU khi đã thu hồi phiên là có chủ đích: token sai thì phiên vẫn bị đóng, nên
	// một lần dùng token rò rỉ sẽ giết luôn phiên đó thay vì cho thử lại.
	if err := hash.CheckToken(refreshToken, storedHash); err != nil {
		return nil, apperrors.New(401, "invalid refresh token", apperrors.ErrUnauthorized)
	}

	return s.issueTokens(ctx, claims.UserID, false)
}

// CreateGuest creates a guest user with limited access.
func (s *AuthService) CreateGuest(ctx context.Context) (*AuthTokens, error) {
	userID := uuid.New()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx,
		`INSERT INTO users (id, auth_provider, is_guest) VALUES ($1, 'guest', TRUE)`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("insert guest: %w", err)
	}

	if err := s.createUserDefaults(ctx, tx, userID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit guest: %w", err)
	}

	return s.issueTokens(ctx, userID, true)
}

// createUserDefaults inserts required related rows for a new user.
// Must be called inside a transaction.
func (s *AuthService) createUserDefaults(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	// error_profiles
	if _, err := tx.Exec(ctx,
		`INSERT INTO error_profiles (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return fmt.Errorf("create error profile: %w", err)
	}
	// subscriptions (free)
	if _, err := tx.Exec(ctx,
		`INSERT INTO subscriptions (user_id, plan, status) VALUES ($1, 'free', 'active') ON CONFLICT DO NOTHING`, userID); err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}
	// streak_records
	if _, err := tx.Exec(ctx,
		`INSERT INTO streak_records (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return fmt.Errorf("create streak record: %w", err)
	}
	return nil
}

// issueTokens signs a new access+refresh pair and stores the session.
func (s *AuthService) issueTokens(ctx context.Context, userID uuid.UUID, isGuest bool) (*AuthTokens, error) {
	sessionID := uuid.New()

	accessToken, err := s.jwtMgr.SignAccess(userID, isGuest)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.jwtMgr.SignRefresh(userID, sessionID)
	if err != nil {
		return nil, err
	}

	// SHA-256 chứ không phải bcrypt: refresh token là JWT dài hơn giới hạn 72 byte của
	// bcrypt, và entropy cao nên không cần hàm băm chậm. Xem `hash.Token`.
	refreshHash := hash.Token(refreshToken)

	expiresAt := time.Now().Add(s.jwtMgr.RefreshTTL())
	_, err = s.db.Exec(ctx,
		`INSERT INTO auth_sessions (id, user_id, refresh_hash, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		sessionID, userID, refreshHash, expiresAt)
	if err != nil {
		return nil, fmt.Errorf("store session: %w", err)
	}

	return &AuthTokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		UserID:       userID,
	}, nil
}

// verifySocialToken verifies an ID token for social providers.
// Returns (externalID, email, displayName, error).
func (s *AuthService) verifySocialToken(_ context.Context, idToken, provider string) (string, string, string, error) {
	if idToken == "" {
		return "", "", "", apperrors.New(401, "invalid id_token", apperrors.ErrUnauthorized)
	}
	_ = provider
	// Never derive an identity from unverified client input. Social auth remains
	// unavailable until provider JWKS, issuer, audience, nonce and expiry are all
	// verified.
	return "", "", "", apperrors.New(
		503, "social authentication is temporarily unavailable", apperrors.ErrServiceUnavail,
	)
}
