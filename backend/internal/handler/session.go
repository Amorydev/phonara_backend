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

// Type aliases from domain to avoid redefining in handler.
type PARawPayload = domain.PARawPayload
type WordScore = domain.WordScore
type PhonemeScore = domain.PhonemeScore

// SessionHandler handles /v1/sessions endpoints.
type SessionHandler struct {
	svc *service.SessionService
}

// NewSessionHandler creates a SessionHandler.
func NewSessionHandler(db *pgxpool.Pool, rdb *goredis.Client, cfg *config.Config) *SessionHandler {
	return &SessionHandler{svc: service.NewSessionService(db, rdb, cfg)}
}

// createSessionRequest is the body for POST /v1/sessions.
type createSessionRequest struct {
	Mode         string `json:"mode" validate:"required,oneof=word sentence minimal_pair read_word shadowing exam"`
	Source       string `json:"source" validate:"omitempty,oneof=recommended free_choice daily onboarding"`
	ScoringLevel string `json:"scoring_level" validate:"omitempty,oneof=easy medium hard"`
}

// Create godoc
//
//	@Summary		Tạo practice session
//	@Description	Mở session luyện tập mới. Server kiểm tra gating (quota freemium). Nếu scoring_level không gửi, dùng default_scoring_level của user.
//	@Tags			Sessions
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		createSessionRequest						true	"Session params"
//	@Success		201		{object}	domain.Response{data=service.SessionDTO}	"Session created"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Failure		402		{object}	domain.Response								"Quota exceeded"
//	@Failure		422		{object}	domain.Response								"Validation error"
//	@Router			/sessions [post]
func (h *SessionHandler) Create(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req createSessionRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	sess, err := h.svc.Create(ctxFromRequest(c), service.CreateSessionInput{
		UserID:       userID,
		Mode:         req.Mode,
		Source:       req.Source,
		ScoringLevel: req.ScoringLevel,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, domain.OK(sess))
}

// Get godoc
//
//	@Summary		Lấy thông tin session
//	@Tags			Sessions
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string									true	"Session UUID"
//	@Success		200	{object}	domain.Response{data=service.SessionDTO}	"Session"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Failure		404	{object}	domain.Response								"Not found"
//	@Router			/sessions/{id} [get]
func (h *SessionHandler) Get(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	sess, err := h.svc.Get(ctxFromRequest(c), userID, c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(sess))
}

// End godoc
//
//	@Summary		Kết thúc session
//	@Description	Đóng session, tính summary_score = avg accuracy từ toàn bộ results.
//	@Tags			Sessions
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string									true	"Session UUID"
//	@Success		200	{object}	domain.Response{data=service.SessionDTO}	"Session ended"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Failure		404	{object}	domain.Response								"Not found"
//	@Router			/sessions/{id}/end [post]
func (h *SessionHandler) End(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	summary, err := h.svc.End(ctxFromRequest(c), userID, c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(summary))
}

// History godoc
//
//	@Summary		Lịch sử practice sessions
//	@Description	Trả 50 session gần nhất. Mỗi session có summary_score và signed URL để replay audio (consent-gated).
//	@Tags			Sessions
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=[]service.SessionDTO}	"Session history"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Router			/sessions/history [get]
func (h *SessionHandler) History(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	items, err := h.svc.History(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(items))
}

// InProgress godoc
//
//	@Summary		Session đang dang dở
//	@Description	Trả session chưa ended_at để client hiển thị "Tiếp tục 2/8" (S09b).
//	@Tags			Sessions
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.SessionDTO}	"In-progress session (null nếu không có)"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/sessions/in-progress [get]
func (h *SessionHandler) InProgress(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	sess, err := h.svc.InProgress(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(sess))
}

// ingestResultRequest is the body for POST /v1/sessions/:id/results.
type ingestResultRequest struct {
	ContentItemID  string              `json:"content_item_id" validate:"omitempty,uuid"`
	MinimalPairID  string              `json:"minimal_pair_id" validate:"omitempty,uuid"`
	ScoringLevel   string              `json:"scoring_level" validate:"required,oneof=easy medium hard"`
	IdempotencyKey string              `json:"idempotency_key" validate:"required"`
	PARaw          domain.PARawPayload `json:"pa_raw" validate:"required"`
	AudioRef       string              `json:"audio_ref"`
}

// IngestResult godoc
//
//	@Summary		Nộp kết quả PA (ingestion)
//	@Description	Nhận PA thô từ Azure Speech SDK. Server áp Level scoring (§3b.0a), sanity-check, lưu DB, enqueue recompute Error Profile.
//	@Description	Idempotent: cùng idempotency_key sẽ không tạo result trùng (BR-QA-03).
//	@Description	Recording fail (no speech/noise): trả 422, không tạo result, không trừ điểm (BR-SCORE-07).
//	@Tags			Sessions
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string												true	"Session UUID"
//	@Param			body	body		ingestResultRequest									true	"PA raw result"
//	@Success		201		{object}	domain.Response{data=service.IngestResultOutput}	"Result stored"
//	@Failure		401		{object}	domain.Response										"Unauthorized"
//	@Failure		404		{object}	domain.Response										"Session not found"
//	@Failure		422		{object}	domain.Response										"Recording failed / validation error"
//	@Router			/sessions/{id}/results [post]
func (h *SessionHandler) IngestResult(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req ingestResultRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	result, err := h.svc.IngestResult(ctxFromRequest(c), service.IngestResultInput{
		UserID:         userID,
		SessionID:      c.Param("id"),
		ContentItemID:  req.ContentItemID,
		MinimalPairID:  req.MinimalPairID,
		ScoringLevel:   req.ScoringLevel,
		IdempotencyKey: req.IdempotencyKey,
		PARaw:          req.PARaw,
		AudioRef:       req.AudioRef,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, domain.OK(result))
}

// IngestBatch godoc
//
//	@Summary		Batch ingest results (offline sync)
//	@Description	Nộp nhiều PA result cùng lúc khi kết nối mạng khôi phục (NFR-REL-03). Tối đa 50 items.
//	@Tags			Sessions
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string												true	"Session UUID"
//	@Param			body	body		object{results=[]ingestResultRequest}				true	"Batch results"
//	@Success		201		{object}	domain.Response{data=[]service.IngestResultOutput}	"Batch processed"
//	@Failure		401		{object}	domain.Response										"Unauthorized"
//	@Failure		422		{object}	domain.Response										"Validation error"
//	@Router			/sessions/{id}/results:batch [post]
func (h *SessionHandler) IngestBatch(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req struct {
		Results []ingestResultRequest `json:"results" validate:"required,min=1,max=50"`
	}
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	inputs := make([]service.IngestResultInput, len(req.Results))
	for i, r := range req.Results {
		inputs[i] = service.IngestResultInput{
			UserID:         userID,
			SessionID:      c.Param("id"),
			ContentItemID:  r.ContentItemID,
			MinimalPairID:  r.MinimalPairID,
			ScoringLevel:   r.ScoringLevel,
			IdempotencyKey: r.IdempotencyKey,
			PARaw:          r.PARaw,
			AudioRef:       r.AudioRef,
		}
	}

	results, err := h.svc.IngestBatch(ctxFromRequest(c), inputs)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, domain.OK(results))
}
