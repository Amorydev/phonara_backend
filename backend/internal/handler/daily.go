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
	svc     *service.DailyService
	mission *service.DailyMissionService
}

// NewDailyHandler creates a DailyHandler.
func NewDailyHandler(db *pgxpool.Pool) *DailyHandler {
	return &DailyHandler{
		svc:     service.NewDailyService(db),
		mission: service.NewDailyMissionService(db),
	}
}

// Today godoc
//
//	@Summary		Daily Challenge hôm nay (summary cho Home)
//	@Description	Trả SUMMARY nhẹ của challenge hôm nay (theo timezone user) cho card ở Home: title, category, banner, item_count, user_status. KHÔNG kèm nội dung item. Khi user mở challenge, dùng challenge_id gọi GET /daily/challenges/{id} để lấy full nội dung (FR-DAILY-01, S27).
//	@Tags			Daily
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.DailyChallengeSummary}	"Today's challenge summary (available=false nếu chưa có challenge)"
//	@Failure		401	{object}	domain.Response										"Unauthorized"
//	@Router			/daily/today [get]
func (h *DailyHandler) Today(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	challenge, err := h.svc.Today(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(challenge))
}

// GetChallenge godoc
//
//	@Summary		Chi tiết Daily Challenge (full nội dung)
//	@Description	Trả TOÀN BỘ challenge theo id trong MỘT request: metadata + danh sách item đã resolve sẵn nội dung (word/sentence kèm IPA & audio, passage kèm các câu). Dùng cho màn challenge; client navigate vào chỉ với challenge_id rồi gọi endpoint này. Hoạt động cho cả challenge hôm nay lẫn challenge cũ deep-link từ history.
//	@Tags			Daily
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string										true	"Challenge UUID"
//	@Success		200	{object}	domain.Response{data=service.DailyChallenge}	"Full challenge"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Failure		404	{object}	domain.Response								"Challenge not found"
//	@Router			/daily/challenges/{id} [get]
func (h *DailyHandler) GetChallenge(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	challenge, err := h.svc.GetChallenge(ctxFromRequest(c), userID, c.Param("id"))
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
//	@Success		200	{object}	domain.Response{data=[]service.DailyHistoryItem}	"Challenge history"
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

// GetMission godoc
//
//	@Summary		Nhiệm vụ hàng ngày (mục tiêu phút)
//	@Description	Trạng thái nhiệm vụ luyện tập hôm nay (theo timezone user): goal_minutes, minutes_done, percent, completed, status. Dùng cho widget "Xuất sắc! 15/15 phút" ở Home.
//	@Tags			Daily
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.DailyMission}	"Mission status"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/daily/mission [get]
func (h *DailyHandler) GetMission(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	mission, err := h.mission.Get(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(mission))
}

// Heartbeat godoc
//
//	@Summary		Cộng dồn thời gian luyện tập
//	@Description	Client gửi DELTA số giây active kể từ lần gửi trước (1–3600). Server cộng dồn vào tổng hôm nay và trả trạng thái mission mới nhất (BR-DAILY). Gửi delta, không gửi tổng tích lũy.
//	@Tags			Daily
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		object{seconds=int}							true	"Active seconds delta"
//	@Success		200		{object}	domain.Response{data=service.DailyMission}	"Updated mission status"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Failure		422		{object}	domain.Response								"Validation error"
//	@Router			/daily/mission/heartbeat [post]
func (h *DailyHandler) Heartbeat(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req struct {
		Seconds int `json:"seconds" validate:"required,min=1,max=3600"`
	}
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	mission, err := h.mission.Heartbeat(ctxFromRequest(c), userID, req.Seconds)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(mission))
}
