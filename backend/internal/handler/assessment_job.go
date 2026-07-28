package handler

import (
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/integration/storage"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// maxAudioBytes chặn upload quá lớn. 30 giây WAV PCM 16-bit 16 kHz ≈ 960 KB;
// 2 MB cho biên độ rộng mà vẫn chặn được người gửi file hàng trăm MB.
const maxAudioBytes = 2 << 20

// AssessmentJobHandler xử lý /v1/assessments (chấm phát âm bất đồng bộ).
type AssessmentJobHandler struct {
	svc *service.AssessmentJobService
}

// NewAssessmentJobHandler tạo handler.
func NewAssessmentJobHandler(
	db *pgxpool.Pool,
	store storage.Store,
	enqueue *asynq.Client,
	gate *service.PracticeGate,
	engineTimeout time.Duration,
) *AssessmentJobHandler {
	return &AssessmentJobHandler{
		svc: service.NewAssessmentJobService(db, store, enqueue, gate, engineTimeout),
	}
}

// Create godoc
//
//	@Summary		Nộp bản ghi để chấm phát âm
//	@Description	Upload audio (WAV PCM 16-bit, mono, 16 kHz) kèm câu mẫu. Server lưu audio, tạo job và chấm bất đồng bộ; client poll `GET /assessments/{id}`.
//	@Description	Gửi lại cùng `Idempotency-Key` trả về job cũ, không tạo job mới và không tốn thêm một lần inference.
//	@Tags			Assessment
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		BearerAuth
//	@Param			audio					formData	file	true	"WAV PCM 16-bit mono 16kHz"
//	@Param			reference_text			formData	string	true	"Câu cần đọc"
//	@Param			session_id				formData	string	false	"UUID practice session"
//	@Param			scoring_level			formData	string	false	"easy|medium|hard"
//	@Param			content_item_id			formData	string	false	"UUID content item"
//	@Param			assessment_question_id	formData	string	false	"UUID câu pre-assessment"
//	@Param			Idempotency-Key			header		string	false	"Khoá chống trùng"
//	@Success		202	{object}	domain.Response{data=service.AssessmentJobDTO}	"Đã nhận, đang chấm"
//	@Failure		400	{object}	domain.Response									"Thiếu audio hoặc reference_text"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Failure		402	{object}	domain.Response									"Hết lượt freemium hôm nay"
//	@Failure		413	{object}	domain.Response									"Audio quá lớn"
//	@Failure		429	{object}	domain.Response									"Gửi quá nhanh"
//	@Failure		503	{object}	domain.Response									"Hàng đợi quá tải"
//	@Router			/assessments [post]
func (h *AssessmentJobHandler) Create(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)

	referenceText := c.FormValue("reference_text")
	if referenceText == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "reference_text là bắt buộc")
	}
	if utf8.RuneCountInString(referenceText) > 1000 {
		return echo.NewHTTPError(http.StatusBadRequest, "reference_text vượt 1000 ký tự")
	}
	scoringLevel := c.FormValue("scoring_level")
	if scoringLevel != "" && scoringLevel != "easy" &&
		scoringLevel != "medium" && scoringLevel != "hard" {
		return echo.NewHTTPError(http.StatusBadRequest, "scoring_level không hợp lệ")
	}
	for name, value := range map[string]string{
		"session_id":             c.FormValue("session_id"),
		"content_item_id":        c.FormValue("content_item_id"),
		"minimal_pair_id":        c.FormValue("minimal_pair_id"),
		"assessment_question_id": c.FormValue("assessment_question_id"),
	} {
		if value != "" {
			if _, err := uuid.Parse(value); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, name+" phải là UUID")
			}
		}
	}
	idempotencyKey := c.Request().Header.Get("Idempotency-Key")
	if len(idempotencyKey) > 128 {
		return echo.NewHTTPError(http.StatusBadRequest, "Idempotency-Key vượt 128 byte")
	}

	fileHeader, err := c.FormFile("audio")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "audio là bắt buộc")
	}
	if fileHeader.Size > maxAudioBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "audio vượt 2 MB")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "không đọc được audio")
	}
	defer src.Close() //nolint:errcheck

	// Vẫn giới hạn khi đọc dù đã kiểm Size: Size do client khai báo qua header, không
	// phải sự thật đã kiểm chứng.
	audio, err := storage.ReadAllLimit(src, maxAudioBytes)
	if err != nil {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, err.Error())
	}

	job, err := h.svc.Create(ctxFromRequest(c), service.CreateAssessmentJobInput{
		UserID:               userID,
		ClientIP:             c.RealIP(),
		SessionID:            c.FormValue("session_id"),
		ReferenceText:        referenceText,
		ScoringLevel:         scoringLevel,
		ContentItemID:        c.FormValue("content_item_id"),
		MinimalPairID:        c.FormValue("minimal_pair_id"),
		AssessmentQuestionID: c.FormValue("assessment_question_id"),
		IdempotencyKey:       idempotencyKey,
		Audio:                audio,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, domain.OK(job))
}

// Get godoc
//
//	@Summary		Lấy kết quả chấm phát âm
//	@Description	Poll trạng thái job. `pending`/`processing` → chưa xong; `done` → có `result`; `failed` → có `error`.
//	@Description	Chi tiết lỗi hệ thống được giấu khỏi client; chỉ lỗi do người dùng (nói quá ngắn, không có tiếng) mới hiển thị nguyên văn.
//	@Tags			Assessment
//	@Produce		json
//	@Security		BearerAuth
//	@Param			id	path		string											true	"Job UUID"
//	@Success		200	{object}	domain.Response{data=service.AssessmentJobDTO}	"Trạng thái job"
//	@Failure		401	{object}	domain.Response									"Unauthorized"
//	@Failure		404	{object}	domain.Response									"Không tìm thấy"
//	@Router			/assessments/{id} [get]
func (h *AssessmentJobHandler) Get(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	job, err := h.svc.Get(ctxFromRequest(c), userID, c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(job))
}
