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

// ProgressHandler handles /v1/progress endpoints.
type ProgressHandler struct {
	svc *service.ProgressService
}

// NewProgressHandler creates a ProgressHandler.
func NewProgressHandler(db *pgxpool.Pool) *ProgressHandler {
	return &ProgressHandler{svc: service.NewProgressService(db)}
}

// Overview godoc
//
//	@Summary		Progress overview
//	@Description	Streak hiện tại, longest_streak, tổng số session (S20).
//	@Tags			Progress
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=domain.ProgressOverviewData}	"Overview data"
//	@Failure		401	{object}	domain.Response										"Unauthorized"
//	@Router			/progress/overview [get]
func (h *ProgressHandler) Overview(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	overview, err := h.svc.Overview(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(overview))
}

// Charts godoc
//
//	@Summary		Score trend chart
//	@Description	Đọc mastery_snapshots để trả điểm theo thời gian cho sparkline (S19/S28).
//	@Tags			Progress
//	@Produce		json
//	@Security		BearerAuth
//	@Param			period	query		string	false	"week hoặc month"
//	@Success		200		{object}	domain.Response{data=domain.ChartsData}	"Chart data points"
//	@Failure		401		{object}	domain.Response							"Unauthorized"
//	@Router			/progress/charts [get]
func (h *ProgressHandler) Charts(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	charts, err := h.svc.Charts(ctxFromRequest(c), userID, c.QueryParam("period"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(charts))
}

// BadgeHandler handles /v1/badges endpoints.
type BadgeHandler struct {
	svc *service.BadgeService
}

// NewBadgeHandler creates a BadgeHandler.
func NewBadgeHandler(db *pgxpool.Pool) *BadgeHandler {
	return &BadgeHandler{svc: service.NewBadgeService(db)}
}

// List godoc
//
//	@Summary		Danh sách badges
//	@Description	Trả earned[] và locked[] với criteria và progress tới badge kế tiếp (FR-STREAK-03, S20).
//	@Tags			Progress
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=domain.BadgeListData}	"earned and locked badges"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/badges [get]
func (h *BadgeHandler) List(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	badges, err := h.svc.List(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(badges))
}

// StreakHandler handles /v1/streak endpoints.
type StreakHandler struct {
	svc *service.StreakService
}

// NewStreakHandler creates a StreakHandler.
func NewStreakHandler(db *pgxpool.Pool, rdb *goredis.Client, cfg *config.Config) *StreakHandler {
	return &StreakHandler{svc: service.NewStreakService(db, rdb)}
}

// CheckIn godoc
//
//	@Summary		Streak check-in hàng ngày
//	@Description	Ghi nhận hoạt động hôm nay theo timezone user. Tăng current_streak nếu liên tiếp, reset về 1 nếu gián đoạn, cập nhật longest_streak (BR-STREAK-01).
//	@Tags			Progress
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.StreakDTO}	"Updated streak"
//	@Failure		401	{object}	domain.Response							"Unauthorized"
//	@Router			/streak/check-in [post]
func (h *StreakHandler) CheckIn(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	streak, err := h.svc.CheckIn(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(streak))
}
