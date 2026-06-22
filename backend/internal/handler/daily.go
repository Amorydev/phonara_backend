package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// DailyHandler handles /v1/daily endpoints.
type DailyHandler struct {
	svc *service.DailyService
}

// NewDailyHandler creates a DailyHandler.
func NewDailyHandler(db *pgxpool.Pool) *DailyHandler {
	return &DailyHandler{svc: service.NewDailyService(db)}
}

// Today godoc
//
//	@Summary		Daily Challenge hôm nay
//	@Description	Trả nội dung challenge ngày hôm nay kèm completion status của user (FR-DAILY-01, S27).
//	@Tags			Daily
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=domain.DailyChallengeData}	"Today's challenge"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Router			/daily/today [get]
func (h *DailyHandler) Today(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	challenge, err := h.svc.Today(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(challenge))
}

// History godoc
//
//	@Summary		Lịch sử Daily Challenge
//	@Description	Trả 30 ngày gần nhất: date, category, status (completed/missed), score (S27 "Thử thách gần đây").
//	@Tags			Daily
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=[]domain.DailyHistoryItem}	"Challenge history"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Router			/daily/history [get]
func (h *DailyHandler) History(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	history, err := h.svc.History(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(history))
}
