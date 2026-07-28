// Package middleware provides Echo middleware for the API server.
package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/pkg/apperrors"
	jwtutil "github.com/phonara/backend/internal/pkg/jwt"
)

const (
	// ContextKeyUserID is the Echo context key for the authenticated user ID.
	ContextKeyUserID  = "user_id"
	ContextKeyIsGuest = "is_guest"
)

// ErrorHandler is the centralized Echo HTTP error handler.
// It maps AppError / sentinel errors to structured JSON responses.
func ErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	code := http.StatusInternalServerError
	msg := "an unexpected error occurred"

	var he *echo.HTTPError
	var ae *apperrors.AppError

	switch {
	case errors.As(err, &ae):
		code = ae.Code
		msg = ae.Message
	case errors.As(err, &he):
		code = he.Code
		if s, ok := he.Message.(string); ok {
			msg = s
		}
	default:
		code = apperrors.HTTPCode(err)
		msg = apperrors.UserMessage(err)
	}

	if code == http.StatusInternalServerError {
		slog.Error("internal server error", "err", err, "path", c.Path())
	}

	_ = c.JSON(code, domain.Err(msg))
}

// RequestLogger returns a middleware that logs every request with structured fields.
func RequestLogger() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			duration := time.Since(start)

			req := c.Request()
			res := c.Response()

			logFn := slog.Info
			if res.Status >= 500 {
				logFn = slog.Error
			} else if res.Status >= 400 {
				logFn = slog.Warn
			}

			logFn("request",
				"method", req.Method,
				"path", req.URL.Path,
				"status", res.Status,
				"duration_ms", duration.Milliseconds(),
				"remote_ip", c.RealIP(),
			)

			return err
		}
	}
}

// JWT returns a middleware that validates the Authorization Bearer JWT and
// populates context with user_id. Returns 401 on missing/invalid token.
func JWT(jwtMgr *jwtutil.Manager, db *pgxpool.Pool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tokenStr := extractBearerToken(c)
			if tokenStr == "" {
				return apperrors.New(http.StatusUnauthorized, "missing authorization token", apperrors.ErrUnauthorized)
			}

			claims, err := jwtMgr.ParseAccess(tokenStr)
			if err != nil {
				if errors.Is(err, jwt.ErrTokenExpired) {
					return apperrors.New(http.StatusUnauthorized, "token expired", apperrors.ErrUnauthorized)
				}
				return apperrors.New(http.StatusUnauthorized, "invalid token", apperrors.ErrUnauthorized)
			}

			var active bool
			if err := db.QueryRow(c.Request().Context(),
				`SELECT EXISTS (
				   SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL
				 )`,
				claims.UserID,
			).Scan(&active); err != nil {
				return fmt.Errorf("check authenticated user: %w", err)
			}
			if !active {
				return apperrors.New(
					http.StatusUnauthorized,
					"invalid token",
					apperrors.ErrUnauthorized,
				)
			}

			c.Set(ContextKeyUserID, claims.UserID)
			c.Set(ContextKeyIsGuest, claims.IsGuest)
			return next(c)
		}
	}
}

// JWTOptional is like JWT but does not reject unauthenticated requests.
// Context will have zero-value user_id if no token is provided.
func JWTOptional(jwtMgr *jwtutil.Manager) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tokenStr := extractBearerToken(c)
			if tokenStr != "" {
				if claims, err := jwtMgr.ParseAccess(tokenStr); err == nil {
					c.Set(ContextKeyUserID, claims.UserID)
					c.Set(ContextKeyIsGuest, claims.IsGuest)
				}
			}
			return next(c)
		}
	}
}

// UserIDFromCtx retrieves the authenticated user ID from Echo context.
// Panics if middleware was not applied — callers behind JWT() are safe.
func UserIDFromCtx(c echo.Context) uuid.UUID {
	v := c.Get(ContextKeyUserID)
	if v == nil {
		return uuid.Nil
	}
	return v.(uuid.UUID)
}

// IsGuestFromCtx returns whether the current user is a guest.
func IsGuestFromCtx(c echo.Context) bool {
	v := c.Get(ContextKeyIsGuest)
	if v == nil {
		return false
	}
	return v.(bool)
}

func extractBearerToken(c echo.Context) string {
	header := c.Request().Header.Get("Authorization")
	if len(header) > 7 && header[:7] == "Bearer " {
		return header[7:]
	}
	return ""
}
