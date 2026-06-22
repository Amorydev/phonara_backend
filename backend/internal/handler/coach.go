package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// CoachHandler handles /v1/coach endpoints.
type CoachHandler struct {
	svc *service.CoachService
}

// NewCoachHandler creates a CoachHandler.
func NewCoachHandler(db *pgxpool.Pool, rdb *goredis.Client) *CoachHandler {
	return &CoachHandler{svc: service.NewCoachService(db, rdb)}
}

// GetProfile godoc
//
//	@Summary		Error Profile — tổng quan phát âm
//	@Description	Trả overall_score, top_errors, phoneme/skill mastery. Dùng cho S07 (onboarding), S19 (coach overview).
//	@Tags			Coach
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.ErrorProfileDTO}	"Error profile"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Router			/coach/profile [get]
func (h *CoachHandler) GetProfile(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	profile, err := h.svc.GetErrorProfile(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(profile))
}

// GetRecommendation godoc
//
//	@Summary		Bài luyện đề xuất
//	@Description	Rank = severity × frequency × L1_importance × goal_fit (BR-COACH-04). Dùng cho S09 hero section.
//	@Tags			Coach
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=[]domain.RecommendationItem}	"Recommended content"
//	@Failure		401	{object}	domain.Response										"Unauthorized"
//	@Router			/coach/recommendation [get]
func (h *CoachHandler) GetRecommendation(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	recs, err := h.svc.GetRecommendation(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(recs))
}

// GetReport godoc
//
//	@Summary		Báo cáo tiến độ theo tuần/tháng
//	@Description	Đọc mastery_snapshots để tính trend ↑↓ và sparkline (FR-COACH-05, S28).
//	@Tags			Coach
//	@Produce		json
//	@Security		BearerAuth
//	@Param			period	query		string	false	"week hoặc month (default: week)"
//	@Success		200		{object}	domain.Response{data=domain.ReportData}	"Progress report"
//	@Failure		401		{object}	domain.Response							"Unauthorized"
//	@Router			/coach/report [get]
func (h *CoachHandler) GetReport(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	period := c.QueryParam("period")
	if period == "" {
		period = "week"
	}
	report, err := h.svc.GetReport(ctxFromRequest(c), userID, period)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(report))
}
