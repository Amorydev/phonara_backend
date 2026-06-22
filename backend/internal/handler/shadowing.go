package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// ShadowingHandler handles /v1/shadowing endpoints.
type ShadowingHandler struct {
	svc *service.ShadowingService
}

// NewShadowingHandler creates a ShadowingHandler.
func NewShadowingHandler(db *pgxpool.Pool) *ShadowingHandler {
	return &ShadowingHandler{svc: service.NewShadowingService(db)}
}

// GetProgress godoc
//
//	@Summary		Tiến độ shadowing passage
//	@Description	Trả current_sentence_index, sentence_status (passed/current/skipped), avg_score.
//	@Tags			Shadowing
//	@Produce		json
//	@Security		BearerAuth
//	@Param			passage_id	path		string												true	"Passage UUID"
//	@Success		200			{object}	domain.Response{data=service.ShadowingProgressDTO}	"Progress"
//	@Failure		401			{object}	domain.Response										"Unauthorized"
//	@Router			/shadowing/{passage_id}/progress [get]
func (h *ShadowingHandler) GetProgress(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	progress, err := h.svc.GetProgress(ctxFromRequest(c), userID, c.Param("passage_id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(progress))
}

// sentenceResultRequest is the body for POST .../sentence-result.
type sentenceResultRequest struct {
	OrderIndex   int                 `json:"order_index" validate:"min=0"`
	ScoringLevel string              `json:"scoring_level" validate:"required,oneof=easy medium hard"`
	PARaw        domain.PARawPayload `json:"pa_raw" validate:"required"`
	AudioRef     string              `json:"audio_ref"`
}

// SubmitSentenceResult godoc
//
//	@Summary		Nộp kết quả câu shadowing
//	@Description	Server áp Level scoring, check ngưỡng 80% (BR-SHAD-02). Trả next_action: advance | retry | skip_available.
//	@Tags			Shadowing
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			passage_id	path		string												true	"Passage UUID"
//	@Param			body		body		sentenceResultRequest								true	"Sentence PA result"
//	@Success		200			{object}	domain.Response{data=service.ShadowingSentenceResult}	"Result"
//	@Failure		401			{object}	domain.Response											"Unauthorized"
//	@Failure		422			{object}	domain.Response											"Validation error"
//	@Router			/shadowing/{passage_id}/sentence-result [post]
func (h *ShadowingHandler) SubmitSentenceResult(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req sentenceResultRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	result, err := h.svc.SubmitSentenceResult(ctxFromRequest(c), service.ShadowingSentenceInput{
		UserID:     userID,
		PassageID:  c.Param("passage_id"),
		OrderIndex: req.OrderIndex,
		ScoringLevel: req.ScoringLevel,
		PARaw:      req.PARaw,
		AudioRef:   req.AudioRef,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(result))
}

// Complete godoc
//
//	@Summary		Hoàn thành passage
//	@Description	Mark passage completed, trả avg_score tổng kết (S26).
//	@Tags			Shadowing
//	@Produce		json
//	@Security		BearerAuth
//	@Param			passage_id	path		string												true	"Passage UUID"
//	@Success		200			{object}	domain.Response{data=domain.ShadowingCompleteData}	"Summary"
//	@Failure		401			{object}	domain.Response										"Unauthorized"
//	@Router			/shadowing/{passage_id}/complete [post]
func (h *ShadowingHandler) Complete(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	summary, err := h.svc.Complete(ctxFromRequest(c), userID, c.Param("passage_id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(summary))
}
