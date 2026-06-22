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

// MinimalPairHandler handles /v1/minimal-pairs endpoints.
type MinimalPairHandler struct {
	svc *service.MinimalPairService
}

// NewMinimalPairHandler creates a MinimalPairHandler.
func NewMinimalPairHandler(db *pgxpool.Pool, rdb *goredis.Client) *MinimalPairHandler {
	return &MinimalPairHandler{svc: service.NewMinimalPairService(db, rdb)}
}

// startDrillRequest is the body for POST /v1/minimal-pairs/listen/start.
type startDrillRequest struct {
	Count int `json:"count" validate:"min=5,max=20"`
}

// StartListenDrill godoc
//
//	@Summary		Bắt đầu listen drill
//	@Description	Tạo phiên nghe phân biệt âm với N cặp ngẫu nhiên, hearts=3 (FR-MP-02, S16).
//	@Tags			MinimalPairs
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		startDrillRequest								true	"Drill config"
//	@Success		201		{object}	domain.Response{data=domain.ListenDrillData}	"Drill created with pair list"
//	@Failure		401		{object}	domain.Response									"Unauthorized"
//	@Failure		422		{object}	domain.Response									"Validation error"
//	@Router			/minimal-pairs/listen/start [post]
func (h *MinimalPairHandler) StartListenDrill(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req startDrillRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	if req.Count == 0 {
		req.Count = 10
	}
	drill, err := h.svc.StartListenDrill(ctxFromRequest(c), userID, req.Count)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, domain.OK(drill))
}

// answerRequest is the body for POST /v1/minimal-pairs/listen/:drill_id/answer.
type answerRequest struct {
	PairID      string `json:"pair_id" validate:"required,uuid"`
	ChosenWord  string `json:"chosen_word" validate:"required"`
}

// SubmitAnswer godoc
//
//	@Summary		Nộp đáp án listen drill
//	@Description	Chấm đúng/sai, trừ heart nếu sai, cập nhật pair_mastery.listen (FR-MP-02).
//	@Tags			MinimalPairs
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			drill_id	path		string										true	"Drill UUID"
//	@Param			body		body		answerRequest								true	"Answer"
//	@Success		200			{object}	domain.Response{data=domain.AnswerResultData}	"is_correct + played_word"
//	@Failure		401			{object}	domain.Response								"Unauthorized"
//	@Failure		404			{object}	domain.Response								"Drill or pair not found"
//	@Router			/minimal-pairs/listen/{drill_id}/answer [post]
func (h *MinimalPairHandler) SubmitAnswer(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req answerRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	result, err := h.svc.SubmitAnswer(ctxFromRequest(c), userID, c.Param("drill_id"), req.PairID, req.ChosenWord)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(result))
}

// GetDrillStatus godoc
//
//	@Summary		Trạng thái listen drill
//	@Tags			MinimalPairs
//	@Produce		json
//	@Security		BearerAuth
//	@Param			drill_id	path		string										true	"Drill UUID"
//	@Success		200			{object}	domain.Response{data=domain.DrillStatusData}	"total_items, correct_count, hearts_left, status"
//	@Failure		401			{object}	domain.Response								"Unauthorized"
//	@Failure		404			{object}	domain.Response								"Not found"
//	@Router			/minimal-pairs/listen/{drill_id} [get]
func (h *MinimalPairHandler) GetDrillStatus(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	status, err := h.svc.GetDrillStatus(ctxFromRequest(c), userID, c.Param("drill_id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(status))
}
