package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/phonara/backend/internal/integration/storage"
)

// MediaHandler phục vụ audio đã sinh (mẫu TTS, bản ghi của người dùng).
type MediaHandler struct {
	store storage.Store
}

// NewMediaHandler tạo handler.
func NewMediaHandler(store storage.Store) *MediaHandler {
	return &MediaHandler{store: store}
}

// Get godoc
//
//	@Summary		Tải file audio
//	@Description	Phục vụ audio mẫu đã sinh sẵn. Đường dẫn lấy từ `sample_audio_url` của câu hỏi.
//	@Tags			Media
//	@Produce		audio/mpeg
//	@Param			ref	path		string	true	"Tham chiếu audio"
//	@Success		200	{file}		binary	"Nội dung audio"
//	@Failure		404	{object}	domain.Response	"Không tìm thấy"
//	@Router			/media/{ref} [get]
func (h *MediaHandler) Get(c echo.Context) error {
	ref := strings.TrimPrefix(c.Param("*"), "/")
	if ref == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "thiếu tham chiếu audio")
	}

	// CHỈ phục vụ audio mẫu. Bản ghi giọng người dùng nằm chung store dưới tiền tố
	// "audio/<user_id>/..." — endpoint này KHÔNG có auth, nên thiếu chốt chặn này thì ai
	// đoán được ref là tải được giọng của người khác.
	//
	// Trả 404 chứ không 403: 403 xác nhận file có tồn tại, giúp người dò biết họ đoán
	// đúng user_id. 404 không tiết lộ gì.
	if !strings.HasPrefix(ref, publicPrefix) {
		return echo.NewHTTPError(http.StatusNotFound, "không tìm thấy audio")
	}

	data, err := h.store.Get(ctxFromRequest(c), ref)
	if errors.Is(err, storage.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "không tìm thấy audio")
	}
	if err != nil {
		// Store tự chặn ref chứa ".." nên lỗi ở đây là sự cố thật, không phải tấn công.
		return err
	}

	// Audio mẫu là bất biến: sinh một lần từ câu tĩnh, không bao giờ đổi nội dung dưới
	// cùng một ref. Cache dài giúp mỗi máy chỉ tải một lần cho mỗi câu.
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	return c.Blob(http.StatusOK, contentTypeFor(ref), data)
}

// publicPrefix là thư mục duy nhất được phục vụ công khai: audio mẫu do TTS sinh, nội
// dung tĩnh, không phải dữ liệu cá nhân.
const publicPrefix = "sample/"

func contentTypeFor(ref string) string {
	switch {
	case strings.HasSuffix(ref, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(ref, ".wav"):
		return "audio/wav"
	case strings.HasSuffix(ref, ".ogg"):
		return "audio/ogg"
	default:
		return "application/octet-stream"
	}
}
