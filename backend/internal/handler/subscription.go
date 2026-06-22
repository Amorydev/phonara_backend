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

// SubscriptionHandler handles /v1/subscription and /v1/iap endpoints.
type SubscriptionHandler struct {
	svc *service.SubscriptionService
}

// NewSubscriptionHandler creates a SubscriptionHandler.
func NewSubscriptionHandler(db *pgxpool.Pool, cfg *config.Config) *SubscriptionHandler {
	return &SubscriptionHandler{svc: service.NewSubscriptionService(db, cfg)}
}

// Get godoc
//
//	@Summary		Trạng thái subscription
//	@Description	Trả plan (free/premium/exam_pack), status, renews_at, store.
//	@Tags			Subscription
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.SubscriptionDTO}	"Subscription info"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Router			/subscription [get]
func (h *SubscriptionHandler) Get(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	sub, err := h.svc.Get(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(sub))
}

// Plans godoc
//
//	@Summary		Danh sách gói đăng ký
//	@Description	Trả pricing plans với giá VND, product_id cho App Store/Google Play (BR-PAY-03, S21).
//	@Tags			Subscription
//	@Produce		json
//	@Success		200	{object}	domain.Response{data=[]domain.PlanItem}	"Plans list with VND pricing"
//	@Router			/subscription/plans [get]
func (h *SubscriptionHandler) Plans(c echo.Context) error {
	plans, err := h.svc.Plans(ctxFromRequest(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(plans))
}

// verifyReceiptRequest is the body for POST /v1/subscription/verify.
type verifyReceiptRequest struct {
	Store   string `json:"store" validate:"required,oneof=app_store google_play"`
	Receipt string `json:"receipt" validate:"required"`
}

// Verify godoc
//
//	@Summary		Verify IAP receipt
//	@Description	Xác minh receipt từ App Store hoặc Google Play, nâng cấp plan nếu hợp lệ (BR-PAY-02).
//	@Tags			Subscription
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		verifyReceiptRequest							true	"Receipt"
//	@Success		200		{object}	domain.Response{data=service.SubscriptionDTO}	"Updated subscription"
//	@Failure		401		{object}	domain.Response									"Unauthorized"
//	@Failure		422		{object}	domain.Response									"Invalid receipt"
//	@Router			/subscription/verify [post]
func (h *SubscriptionHandler) Verify(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req verifyReceiptRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	sub, err := h.svc.Verify(ctxFromRequest(c), userID, req.Store, req.Receipt)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(sub))
}

// Restore godoc
//
//	@Summary		Restore Purchase (bắt buộc store)
//	@Description	Khôi phục entitlement bằng cách re-verify với store (FR-PAY-02). Bắt buộc theo App Store / Google Play policy.
//	@Tags			Subscription
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		verifyReceiptRequest							true	"Receipt"
//	@Success		200		{object}	domain.Response{data=service.SubscriptionDTO}	"Restored subscription"
//	@Failure		401		{object}	domain.Response									"Unauthorized"
//	@Router			/subscription/restore [post]
func (h *SubscriptionHandler) Restore(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req verifyReceiptRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	sub, err := h.svc.Restore(ctxFromRequest(c), userID, req.Store, req.Receipt)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(sub))
}

// WebhookApple godoc
//
//	@Summary		Apple server notifications webhook
//	@Description	Nhận server notifications từ Apple (subscription renewed, canceled, refunded).
//	@Tags			Subscription
//	@Accept			json
//	@Produce		json
//	@Success		200	{object}	domain.Response{data=domain.MessageData}	"ok"
//	@Router			/iap/webhook/apple [post]
func (h *SubscriptionHandler) WebhookApple(c echo.Context) error {
	body := make(map[string]any)
	_ = c.Bind(&body)
	if err := h.svc.HandleAppleWebhook(ctxFromRequest(c), body); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(map[string]string{"message": "ok"}))
}

// WebhookGoogle godoc
//
//	@Summary		Google Play RTDN webhook
//	@Description	Nhận Real-Time Developer Notifications từ Google Play (subscription events).
//	@Tags			Subscription
//	@Accept			json
//	@Produce		json
//	@Success		200	{object}	domain.Response{data=domain.MessageData}	"ok"
//	@Router			/iap/webhook/google [post]
func (h *SubscriptionHandler) WebhookGoogle(c echo.Context) error {
	body := make(map[string]any)
	_ = c.Bind(&body)
	if err := h.svc.HandleGoogleWebhook(ctxFromRequest(c), body); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(map[string]string{"message": "ok"}))
}

// Quota godoc
//
//	@Summary		Freemium quota hôm nay
//	@Description	Trả items_used, daily_limit, remaining. Premium user trả is_premium=true, limit=-1 (BR-FREE-01).
//	@Tags			Subscription
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.QuotaDTO}	"Quota"
//	@Failure		401	{object}	domain.Response							"Unauthorized"
//	@Router			/freemium/quota [get]
func (h *SubscriptionHandler) Quota(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	quota, err := h.svc.GetQuota(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(quota))
}
