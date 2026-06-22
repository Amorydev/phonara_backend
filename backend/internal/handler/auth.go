package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/pkg/apperrors"
	"github.com/phonara/backend/internal/pkg/hash"
	jwtutil "github.com/phonara/backend/internal/pkg/jwt"
	"github.com/phonara/backend/internal/service"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	jwtMgr *jwtutil.Manager
	db     *pgxpool.Pool
	rdb    *goredis.Client
	svc    *service.AuthService
}

// NewAuthHandler creates an AuthHandler.
func NewAuthHandler(jwtMgr *jwtutil.Manager, db *pgxpool.Pool, rdb *goredis.Client) *AuthHandler {
	return &AuthHandler{
		jwtMgr: jwtMgr,
		db:     db,
		rdb:    rdb,
		svc:    service.NewAuthService(db, rdb, jwtMgr),
	}
}

// registerRequest is the body for POST /v1/auth/register.
type registerRequest struct {
	Provider    string `json:"provider" validate:"required,oneof=email google apple"`
	Email       string `json:"email" validate:"omitempty,email"`
	Password    string `json:"password" validate:"omitempty,min=8"`
	IDToken     string `json:"id_token" validate:"omitempty"`
	DisplayName string `json:"display_name"`
}

// loginRequest is the body for POST /v1/auth/login.
type loginRequest struct {
	Provider string `json:"provider" validate:"required,oneof=email google apple"`
	Email    string `json:"email" validate:"omitempty,email"`
	Password string `json:"password" validate:"omitempty"`
	IDToken  string `json:"id_token" validate:"omitempty"`
}

// refreshRequest is the body for POST /v1/auth/refresh.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// tokenResponse is returned after successful auth.
type tokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       uuid.UUID `json:"user_id"`
	IsGuest      bool      `json:"is_guest,omitempty"`
}

// Register godoc
//
//	@Summary		Đăng ký tài khoản
//	@Description	Tạo tài khoản mới qua email/Google/Apple. Tự động tạo error_profile, subscription(free), streak_record.
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		registerRequest								true	"Registration payload"
//	@Success		201		{object}	domain.Response{data=domain.TokenData}			"Tokens issued"
//	@Failure		400		{object}	domain.Response								"Missing fields"
//	@Failure		409		{object}	domain.Response								"Email already registered"
//	@Failure		422		{object}	domain.Response								"Validation error"
//	@Router			/auth/register [post]
func (h *AuthHandler) Register(c echo.Context) error {
	var req registerRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	switch req.Provider {
	case "email":
		if req.Email == "" || req.Password == "" {
			return apperrors.New(http.StatusBadRequest, "email and password required for email provider", apperrors.ErrBadRequest)
		}
	case "google", "apple":
		if req.IDToken == "" {
			return apperrors.New(http.StatusBadRequest, "id_token required for social providers", apperrors.ErrBadRequest)
		}
	}

	resp, err := h.svc.Register(ctxFromRequest(c), service.RegisterInput{
		Provider:    req.Provider,
		Email:       req.Email,
		Password:    req.Password,
		IDToken:     req.IDToken,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, domain.OK(tokenResponse{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresAt:    resp.ExpiresAt,
		UserID:       resp.UserID,
	}))
}

// Login godoc
//
//	@Summary		Đăng nhập
//	@Description	Xác thực người dùng qua email/Google/Apple. Trả access_token (15m) và refresh_token (7d).
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		loginRequest							true	"Login payload"
//	@Success		200		{object}	domain.Response{data=domain.TokenData}		"Tokens issued"
//	@Failure		401		{object}	domain.Response							"Invalid credentials"
//	@Failure		422		{object}	domain.Response							"Validation error"
//	@Router			/auth/login [post]
func (h *AuthHandler) Login(c echo.Context) error {
	var req loginRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	resp, err := h.svc.Login(ctxFromRequest(c), service.LoginInput{
		Provider: req.Provider,
		Email:    req.Email,
		Password: req.Password,
		IDToken:  req.IDToken,
	})
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, domain.OK(tokenResponse{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresAt:    resp.ExpiresAt,
		UserID:       resp.UserID,
	}))
}

// Refresh godoc
//
//	@Summary		Refresh token
//	@Description	Revoke refresh token cũ và issue cặp token mới. Client dùng khi access_token hết hạn.
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		refreshRequest							true	"Refresh token"
//	@Success		200		{object}	domain.Response{data=domain.TokenData}		"New tokens"
//	@Failure		401		{object}	domain.Response							"Invalid or expired refresh token"
//	@Router			/auth/refresh [post]
func (h *AuthHandler) Refresh(c echo.Context) error {
	var req refreshRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	resp, err := h.svc.Refresh(ctxFromRequest(c), req.RefreshToken)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, domain.OK(tokenResponse{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresAt:    resp.ExpiresAt,
		UserID:       resp.UserID,
	}))
}

// Guest godoc
//
//	@Summary		Tạo guest user
//	@Description	Tạo tài khoản khách tạm thời (BR-FREE-04). Giới hạn tính năng, có thể upgrade sau.
//	@Tags			Auth
//	@Produce		json
//	@Success		201	{object}	domain.Response{data=domain.TokenData}		"Guest tokens"
//	@Failure		500	{object}	domain.Response							"Internal error"
//	@Router			/auth/guest [post]
func (h *AuthHandler) Guest(c echo.Context) error {
	resp, err := h.svc.CreateGuest(ctxFromRequest(c))
	if err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, domain.OK(tokenResponse{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresAt:    resp.ExpiresAt,
		UserID:       resp.UserID,
		IsGuest:      true,
	}))
}

// ensure hash is referenced (used in service layer)
var _ = hash.Password
var _ = errors.Is
var _ = pgx.ErrNoRows
