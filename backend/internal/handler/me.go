package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// MeHandler handles /v1/me endpoints.
type MeHandler struct {
	svc *service.MeService
}

// NewMeHandler creates a MeHandler.
func NewMeHandler(db *pgxpool.Pool) *MeHandler {
	return &MeHandler{svc: service.NewMeService(db)}
}

// GetMe godoc
//
//	@Summary		Lấy profile người dùng
//	@Description	Trả về thông tin profile: goal, level, accent, scoring_level, timezone, consent flags.
//	@Tags			Me
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.UserProfile}	"Profile"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Failure		404	{object}	domain.Response								"User not found"
//	@Router			/me [get]
func (h *MeHandler) GetMe(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	user, err := h.svc.GetProfile(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(user))
}

// updateMeRequest is the body for PATCH /v1/me.
type updateMeRequest struct {
	Goal                string  `json:"goal" validate:"omitempty,oneof=communication interview ielts_toeic beginner"`
	Level               string  `json:"level" validate:"omitempty,oneof=beginner intermediate advanced"`
	TargetAccent        string  `json:"target_accent" validate:"omitempty,oneof=US UK"`
	DailyGoalItems      *int    `json:"daily_goal_items" validate:"omitempty,min=1,max=50"`
	DailyGoalMinutes    *int    `json:"daily_goal_minutes" validate:"omitempty,min=1,max=120"`
	DefaultScoringLevel string  `json:"default_scoring_level" validate:"omitempty,oneof=easy medium hard"`
	DisplayName         *string `json:"display_name"`
	AvatarURL           *string `json:"avatar_url" validate:"omitempty,url"`
	Timezone            *string `json:"timezone"`
}

// UpdateMe godoc
//
//	@Summary		Cập nhật profile
//	@Description	Cập nhật goal, level, accent, daily_goal_items, daily_goal_minutes, default_scoring_level, display_name, avatar_url, timezone. Chỉ gửi field cần đổi.
//	@Tags			Me
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		updateMeRequest								true	"Profile update"
//	@Success		200		{object}	domain.Response{data=service.UserProfile}	"Updated profile"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Failure		422		{object}	domain.Response								"Validation error"
//	@Router			/me [patch]
func (h *MeHandler) UpdateMe(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req updateMeRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}

	user, err := h.svc.UpdateProfile(ctxFromRequest(c), userID, service.UpdateProfileInput{
		Goal:                req.Goal,
		Level:               req.Level,
		TargetAccent:        req.TargetAccent,
		DailyGoalItems:      req.DailyGoalItems,
		DailyGoalMinutes:    req.DailyGoalMinutes,
		DefaultScoringLevel: req.DefaultScoringLevel,
		DisplayName:         req.DisplayName,
		AvatarURL:           req.AvatarURL,
		Timezone:            req.Timezone,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(user))
}

// DeleteMe godoc
//
//	@Summary		Xóa tài khoản
//	@Description	Soft-delete tài khoản và enqueue async job xóa toàn bộ audio trên S3 (BR-PRIV-02).
//	@Tags			Me
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=domain.MessageData}	"Deletion initiated"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/me [delete]
func (h *MeHandler) DeleteMe(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	if err := h.svc.DeleteAccount(ctxFromRequest(c), userID); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(map[string]string{"message": "account deletion initiated"}))
}

// Sync godoc
//
//	@Summary		Đồng bộ đa thiết bị
//	@Description	Trả về payload sync cho đa thiết bị (NFR-REL-04).
//	@Tags			Me
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=domain.SyncData}	"Sync data"
//	@Failure		401	{object}	domain.Response							"Unauthorized"
//	@Router			/me/sync [post]
func (h *MeHandler) Sync(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	data, err := h.svc.SyncData(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(data))
}

// notifPrefsRequest is the body for PATCH /v1/me/notifications.
type notifPrefsRequest struct {
	PracticeReminder *struct {
		Enabled bool   `json:"enabled"`
		Time    string `json:"time" validate:"omitempty"`
	} `json:"practice_reminder"`
	StreakReminder *struct {
		Enabled bool `json:"enabled"`
	} `json:"streak_reminder"`
}

// GetNotifications godoc
//
//	@Summary		Lấy notification preferences
//	@Description	Đọc cài đặt nhắc luyện tập và nhắc streak (S08/S22).
//	@Tags			Me
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.NotifPrefs}	"Notification preferences"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/me/notifications [get]
func (h *MeHandler) GetNotifications(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	prefs, err := h.svc.GetNotifPrefs(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(prefs))
}

// UpdateNotifications godoc
//
//	@Summary		Cập nhật notification preferences
//	@Description	Bật/tắt practice_reminder và streak_reminder, đặt giờ nhắc (S22).
//	@Tags			Me
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		notifPrefsRequest						true	"Notification preferences"
//	@Success		200		{object}	domain.Response{data=service.NotifPrefs}	"Updated"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Router			/me/notifications [patch]
func (h *MeHandler) UpdateNotifications(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req notifPrefsRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	prefs, err := h.svc.UpdateNotifPrefs(ctxFromRequest(c), userID, req)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(prefs))
}

// registerDeviceRequest is the body for POST /v1/me/devices.
type registerDeviceRequest struct {
	PushToken string `json:"push_token" validate:"required"`
	Platform  string `json:"platform" validate:"required,oneof=ios android"`
}

// RegisterDevice godoc
//
//	@Summary		Đăng ký push token
//	@Description	Lưu APNs (iOS) hoặc FCM (Android) push token cho thiết bị hiện tại.
//	@Tags			Me
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		registerDeviceRequest					true	"Device info"
//	@Success		201		{object}	domain.Response{data=domain.MessageData}	"Device registered"
//	@Failure		401		{object}	domain.Response							"Unauthorized"
//	@Failure		422		{object}	domain.Response							"Validation error"
//	@Router			/me/devices [post]
func (h *MeHandler) RegisterDevice(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req registerDeviceRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	if err := h.svc.RegisterDevice(ctxFromRequest(c), userID, req.PushToken, req.Platform); err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, domain.OK(map[string]string{"message": "device registered"}))
}

// GetPrivacy godoc
//
//	@Summary		Lấy consent flags
//	@Description	Đọc cài đặt quyền riêng tư: lưu bản ghi và dùng để cải thiện sản phẩm (BR-PRIV-01/03, S23).
//	@Tags			Me
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.PrivacyPrefs}	"Privacy settings"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/me/privacy [get]
func (h *MeHandler) GetPrivacy(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	prefs, err := h.svc.GetPrivacy(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(prefs))
}

// privacyRequest is the body for PATCH /v1/me/privacy.
type privacyRequest struct {
	AllowRecordingForImprovement *bool `json:"allow_recording_for_improvement"`
	SaveRecordingHistory         *bool `json:"save_recording_history"`
}

// UpdatePrivacy godoc
//
//	@Summary		Cập nhật consent flags
//	@Description	Bật/tắt allow_recording_for_improvement và save_recording_history. Chi phối việc lưu audio_ref.
//	@Tags			Me
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		privacyRequest								true	"Privacy flags"
//	@Success		200		{object}	domain.Response{data=service.PrivacyPrefs}	"Updated"
//	@Failure		401		{object}	domain.Response								"Unauthorized"
//	@Router			/me/privacy [patch]
func (h *MeHandler) UpdatePrivacy(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	var req privacyRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	prefs, err := h.svc.UpdatePrivacy(ctxFromRequest(c), userID, req.AllowRecordingForImprovement, req.SaveRecordingHistory)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(prefs))
}

// ExportData godoc
//
//	@Summary		Xuất dữ liệu cá nhân (GDPR)
//	@Description	Enqueue job gom toàn bộ dữ liệu user thành file tải về (BR-PRIV-02, S23).
//	@Tags			Me
//	@Produce		json
//	@Security		BearerAuth
//	@Success		202	{object}	domain.Response{data=domain.MessageData}	"Export request accepted"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/me/export [post]
func (h *MeHandler) ExportData(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	if err := h.svc.EnqueueExport(ctxFromRequest(c), userID); err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, domain.OK(map[string]string{"message": "export request accepted, you will receive a download link"}))
}

// DeleteHistory godoc
//
//	@Summary		Xóa lịch sử luyện tập
//	@Description	Xóa toàn bộ sessions/results/audio, giữ nguyên tài khoản (S23).
//	@Tags			Me
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=domain.MessageData}	"History deleted"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Router			/me/history [delete]
func (h *MeHandler) DeleteHistory(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	if err := h.svc.DeletePracticeHistory(ctxFromRequest(c), userID); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(map[string]string{"message": "practice history deleted"}))
}
