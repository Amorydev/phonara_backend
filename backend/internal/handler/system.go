package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// SystemHandler handles system-level endpoints.
type SystemHandler struct {
	svc *service.SystemService
}

// NewSystemHandler creates a SystemHandler.
func NewSystemHandler(db *pgxpool.Pool) *SystemHandler {
	return &SystemHandler{svc: service.NewSystemService(db)}
}

// feedbackRequest is the body for POST /v1/feedback.
type feedbackRequest struct {
	Type    string         `json:"type" validate:"required,oneof=feedback bug support"`
	Message string         `json:"message" validate:"required,min=1,max=2000"`
	Context map[string]any `json:"context"`
}

// Feedback godoc
//
//	@Summary		Gửi feedback / báo lỗi
//	@Description	Lưu feedback/bug/support report kèm context (app version, device, screen). S22/S24.
//	@Tags			System
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		feedbackRequest								true	"Feedback"
//	@Success		201		{object}	domain.Response{data=domain.MessageData}	"Received"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Failure		422		{object}	domain.Response								"Validation error"
//	@Router			/feedback [post]
func (h *SystemHandler) Feedback(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req feedbackRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	if err := h.svc.SubmitFeedback(ctxFromRequest(c), userID, req.Type, req.Message, req.Context); err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, domain.OK(map[string]string{"message": "feedback received"}))
}

// AppConfig godoc
//
//	@Summary		App configuration flags
//	@Description	Trả min_version (force-update) và feature flags "Sắp có" (S09b/S22).
//	@Tags			System
//	@Produce		json
//	@Success		200	{object}	domain.Response{data=domain.AppConfigData}	"Config key-value map"
//	@Router			/app-config [get]
func (h *SystemHandler) AppConfig(c echo.Context) error {
	cfg, err := h.svc.GetAppConfig(ctxFromRequest(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(cfg))
}

// Legal godoc
//
//	@Summary		Legal documents (Terms / Privacy)
//	@Description	Trả phiên bản mới nhất của Terms of Service hoặc Privacy Policy (S02/S23).
//	@Tags			System
//	@Produce		json
//	@Param			doc_type	path		string									true	"terms hoặc privacy"
//	@Success		200			{object}	domain.Response{data=domain.LegalDocData}	"Legal document (markdown)"
//	@Failure		404			{object}	domain.Response								"Not found"
//	@Router			/legal/{doc_type} [get]
func (h *SystemHandler) Legal(c echo.Context) error {
	docType := c.Param("doc_type")
	doc, err := h.svc.GetLegalDoc(ctxFromRequest(c), docType)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(doc))
}

// analyticsEventRequest is the body for POST /v1/events.
type analyticsEventRequest struct {
	EventName  string         `json:"event_name" validate:"required"`
	Properties map[string]any `json:"properties"`
}

// IngestAnalytics godoc
//
//	@Summary		Ingest analytics event
//	@Description	Ghi analytics event vào analytics_events (15 events từ doc 10: practice_item_scored, fix_guide_viewed, recording_failed…).
//	@Tags			System
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		analyticsEventRequest						true	"Event"
//	@Success		202		{object}	domain.Response{data=domain.MessageData}	"Accepted"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Failure		422		{object}	domain.Response								"Validation error"
//	@Router			/events [post]
func (h *SystemHandler) IngestAnalytics(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req analyticsEventRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	if err := h.svc.IngestEvent(ctxFromRequest(c), userID, req.EventName, req.Properties); err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, domain.OK(map[string]string{"message": "ok"}))
}
