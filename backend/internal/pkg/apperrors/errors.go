// Package apperrors defines sentinel errors and HTTP error mapping for the application.
package apperrors

import (
	"errors"
	"net/http"
)

// Sentinel errors — use errors.Is() to match.
var (
	ErrNotFound       = errors.New("not found")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrForbidden      = errors.New("forbidden")
	ErrConflict       = errors.New("conflict")
	ErrBadRequest     = errors.New("bad request")
	ErrUnprocessable  = errors.New("unprocessable entity")
	ErrQuotaExceeded  = errors.New("quota exceeded")
	ErrRateLimited    = errors.New("rate limited")
	ErrServiceUnavail = errors.New("service unavailable")
	ErrInternal       = errors.New("internal server error")
)

// AppError carries an HTTP status, a user-facing message, and an optional
// internal cause. Handlers map AppError to JSON responses.
type AppError struct {
	Code    int
	Message string
	Cause   error
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *AppError) Unwrap() error { return e.Cause }

// New creates an AppError wrapping a sentinel with an HTTP code and message.
func New(code int, message string, cause error) *AppError {
	return &AppError{Code: code, Message: message, Cause: cause}
}

// HTTPCode maps a sentinel error to an HTTP status code.
func HTTPCode(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrBadRequest):
		return http.StatusBadRequest
	case errors.Is(err, ErrUnprocessable):
		return http.StatusUnprocessableEntity
	case errors.Is(err, ErrQuotaExceeded):
		return http.StatusPaymentRequired
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrServiceUnavail):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// UserMessage returns a safe message for the given error (no internal details).
func UserMessage(err error) string {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae.Message
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return "resource not found"
	case errors.Is(err, ErrUnauthorized):
		return "authentication required"
	case errors.Is(err, ErrForbidden):
		return "access denied"
	case errors.Is(err, ErrConflict):
		return "resource already exists"
	case errors.Is(err, ErrBadRequest):
		return "invalid request"
	case errors.Is(err, ErrQuotaExceeded):
		return "daily practice limit reached"
	case errors.Is(err, ErrRateLimited):
		return "too many requests, slow down"
	case errors.Is(err, ErrServiceUnavail):
		return "service temporarily unavailable"
	default:
		return "an unexpected error occurred"
	}
}
