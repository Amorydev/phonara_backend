package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// SpeechHandler handles /v1/speech endpoints.
type SpeechHandler struct {
	svc *service.TokenBrokerService
}

// NewSpeechHandler creates a SpeechHandler.
func NewSpeechHandler(db *pgxpool.Pool, rdb *goredis.Client, cfg *config.Config) *SpeechHandler {
	return &SpeechHandler{svc: service.NewTokenBrokerService(db, rdb, cfg)}
}

// issueTokenRequest is the body for POST /v1/speech/token.
type issueTokenRequest struct {
	Engine    string `json:"engine" validate:"required,oneof=azure speechace"`
	SessionID string `json:"session_id" validate:"required,uuid"`
}

// IssueToken godoc
//
//	@Summary		Cấp speech token (§3c)
//	@Description	Cấp short-lived token (30–60s) cho Azure Speech hoặc Speechace.
//	@Description	Áp dụng 7 lớp phòng thủ chi phí: rate-limit user/IP, freemium quota, cost circuit breaker.
//	@Tags			Speech
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		issueTokenRequest								true	"Token request"
//	@Success		200		{object}	domain.Response{data=service.SpeechTokenResult}	"Token issued"
//	@Failure		401		{object}	domain.Response									"Unauthorized"
//	@Failure		402		{object}	domain.Response									"Freemium quota exceeded"
//	@Failure		422		{object}	domain.Response									"Validation error"
//	@Failure		429		{object}	domain.Response									"Rate limit exceeded"
//	@Failure		503		{object}	domain.Response									"Cost circuit breaker triggered"
//	@Router			/speech/token [post]
func (h *SpeechHandler) IssueToken(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req issueTokenRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	result, err := h.svc.IssueToken(ctxFromRequest(c), service.IssueTokenInput{
		UserID:    userID,
		Engine:    req.Engine,
		SessionID: req.SessionID,
		ClientIP:  c.RealIP(),
	})
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, domain.OK(result))
}
