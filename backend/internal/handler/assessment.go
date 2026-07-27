package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/service"
)

// validCEFRLevels are the accepted values for the optional `level` filter.
var validCEFRLevels = map[string]bool{
	"A1": true, "A2": true, "B1": true, "B2": true, "C1": true, "C2": true,
}

// AssessmentHandler handles /v1/assessments endpoints.
type AssessmentHandler struct {
	svc *service.AssessmentService
}

// NewAssessmentHandler creates an AssessmentHandler.
func NewAssessmentHandler(db *pgxpool.Pool) *AssessmentHandler {
	return &AssessmentHandler{svc: service.NewAssessmentService(db)}
}

// GetPreAssessment godoc
//
//	@Summary		Bộ câu hỏi Pre-Assessment
//	@Description	Lấy toàn bộ bộ câu hỏi Pre-Assessment trong onboarding bằng MỘT request. Response gồm metadata của bộ đề + danh sách câu hỏi đã sắp xếp theo `order`. Hỗ trợ chọn bộ đề cụ thể qua `code` hoặc lọc theo cấp độ CEFR qua `level` để mở rộng (A1, A2, B1…).
//	@Tags			Assessment
//	@Produce		json
//	@Security		BearerAuth
//	@Param			code	query		string	false	"Slug bộ đề cụ thể (vd: pre_assessment_default)"
//	@Param			level	query		string	false	"Cấp độ CEFR"	Enums(A1, A2, B1, B2, C1, C2)
//	@Success		200	{object}	domain.Response{data=service.AssessmentSet}	"Bộ câu hỏi pre-assessment"
//	@Failure		400	{object}	domain.Response								"Tham số không hợp lệ"
//	@Failure		401	{object}	domain.Response								"Unauthorized"
//	@Failure		404	{object}	domain.Response								"Chưa có bộ đề active"
//	@Router			/assessments/pre-assessment [get]
func (h *AssessmentHandler) GetPreAssessment(c echo.Context) error {
	level := c.QueryParam("level")
	if level != "" && !validCEFRLevels[level] {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid level: must be one of A1, A2, B1, B2, C1, C2")
	}

	set, err := h.svc.GetPreAssessment(ctxFromRequest(c), service.AssessmentFilter{
		Code:  c.QueryParam("code"),
		Level: level,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(set))
}
