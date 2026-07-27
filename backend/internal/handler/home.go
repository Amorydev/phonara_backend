package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/domain"
	custmiddleware "github.com/phonara/backend/internal/server/middleware"
	"github.com/phonara/backend/internal/service"
)

// HomeHandler handles the /v1/home aggregate endpoint.
type HomeHandler struct {
	svc *service.HomeService
}

// NewHomeHandler creates a HomeHandler.
func NewHomeHandler(db *pgxpool.Pool) *HomeHandler {
	return &HomeHandler{svc: service.NewHomeService(db)}
}

// Get godoc
//
//	@Summary		Màn hình Home (aggregate)
//	@Description	Gộp toàn bộ dữ liệu Home trong MỘT request: header (lời chào + streak), nhiệm vụ hàng ngày (mục tiêu phút), thử thách hôm nay (summary), và danh sách card chế độ luyện tập. Các phần degrade độc lập — phần nào lỗi trả null, không làm hỏng cả Home.
//	@Tags			Home
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	domain.Response{data=service.Home}	"Home payload"
//	@Failure		401	{object}	domain.Response						"Unauthorized"
//	@Router			/home [get]
func (h *HomeHandler) Get(c echo.Context) error {
	userID := custmiddleware.UserIDFromCtx(c)
	home, err := h.svc.Get(ctxFromRequest(c), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, domain.OK(home))
}
