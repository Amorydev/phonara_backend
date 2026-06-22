package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// ExamHandler handles /v1/exam endpoints.
type ExamHandler struct {
	svc *service.ExamService
}

// NewExamHandler creates an ExamHandler.
func NewExamHandler(db *pgxpool.Pool, cfg *config.Config) *ExamHandler {
	return &ExamHandler{svc: service.NewExamService(db, cfg)}
}

// ListPrompts godoc
//
//	@Summary		Danh sách đề thi
//	@Description	Lấy exam prompts IELTS/TOEIC theo type và part.
//	@Tags			Exam
//	@Produce		json
//	@Security		BearerAuth
//	@Param			type	query		string	false	"ielts hoặc toeic"
//	@Param			part	query		string	false	"part1, part2, part3…"
//	@Success		200		{object}	domain.Response{data=[]domain.ExamPromptItem}	"Prompts list"
//	@Failure		401		{object}	domain.Response									"Unauthorized"
//	@Router			/exam/prompts [get]
func (h *ExamHandler) ListPrompts(c echo.Context) error {
	prompts, err := h.svc.ListPrompts(ctxFromRequest(c), c.QueryParam("type"), c.QueryParam("part"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(prompts))
}

// Create godoc
//
//	@Summary		Tạo exam session
//	@Description	Mở session thi, kiểm tra gating Exam Pack (BR-PAY-06).
//	@Tags			Exam
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		object{prompt_id=string}							true	"Prompt ID"
//	@Success		201		{object}	domain.Response{data=domain.ExamSessionData}		"Session created"
//	@Failure		401		{object}	domain.Response										"Unauthorized"
//	@Failure		402		{object}	domain.Response										"Exam Pack required"
//	@Router			/exam/sessions [post]
func (h *ExamHandler) Create(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req struct {
		PromptID string `json:"prompt_id" validate:"required,uuid"`
	}
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	sess, err := h.svc.Create(ctxFromRequest(c), userID, req.PromptID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, domain.OK(sess))
}

// Submit godoc
//
//	@Summary		Nộp bài thi (upload audio)
//	@Description	Client upload audio_ref, server enqueue async Speechace scoring. Client KHÔNG tự chấm band (§3b.2 trust boundary).
//	@Tags			Exam
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id		path		string								true	"Session UUID"
//	@Param			body	body		object{audio_ref=string}			true	"Audio reference"
//	@Success		202		{object}	domain.Response{data=domain.ExamSessionData}	"Submitted for scoring"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Failure		404		{object}	domain.Response								"Session not found"
//	@Router			/exam/sessions/{id}/submit [post]
func (h *ExamHandler) Submit(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req struct {
		AudioRef string `json:"audio_ref" validate:"required"`
	}
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	result, err := h.svc.Submit(ctxFromRequest(c), userID, c.Param("id"), req.AudioRef)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, domain.OK(result))
}

// Report godoc
//
//	@Summary		Báo cáo kết quả thi
//	@Description	Trả band_score + cefr_level + criteria sau khi Speechace đã chấm xong (S33).
//	@Tags			Exam
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string									true	"Session UUID"
//	@Success		200	{object}	domain.Response{data=domain.ExamReportData}	"Exam report"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Failure		404	{object}	domain.Response								"Not found"
//	@Router			/exam/sessions/{id}/report [get]
func (h *ExamHandler) Report(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	report, err := h.svc.Report(ctxFromRequest(c), userID, c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(report))
}

// ListSessions godoc
//
//	@Summary		Lịch sử exam sessions
//	@Description	Trả 20 session gần nhất với band_score và cefr_level (save/share, S33).
//	@Tags			Exam
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=[]domain.ExamSessionData}	"Sessions list"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Router			/exam/sessions [get]
func (h *ExamHandler) ListSessions(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	sessions, err := h.svc.ListSessions(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(sessions))
}
