package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// Endpoint media KHÔNG có auth. Nó chỉ được phép phục vụ audio mẫu — bản ghi giọng người
// dùng nằm chung store dưới "audio/<user_id>/...", và nếu lọt thì ai đoán được ref là tải
// được giọng người khác.
//
// Test này chặn đúng lỗ hổng đó. Đừng nới điều kiện prefix mà không thêm auth.
func TestMediaRejectsNonSamplePaths(t *testing.T) {
	blocked := []string{
		"audio/31fca976-3fd7-44b5-9fec-0063a619ef6a/2026/07/job.wav",
		"audio/other-user/recording.wav",
		"../../etc/passwd",
		"samples/x.mp3", // gần giống nhưng KHÔNG phải "sample/"
		"",
	}

	h := NewMediaHandler(nil) // không chạm store: phải bị chặn trước đó
	for _, ref := range blocked {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/v1/media/"+ref, nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("*")
		c.SetParamValues(ref)

		err := h.Get(c)
		if err == nil {
			t.Errorf("ref %q phải bị từ chối, nhưng được chấp nhận", ref)
			continue
		}
		he, ok := err.(*echo.HTTPError)
		if !ok {
			t.Errorf("ref %q: kỳ vọng HTTPError, nhận %T", ref, err)
			continue
		}
		// 404 chứ không 403: 403 xác nhận file tồn tại và giúp người dò biết đã đoán đúng.
		if ref != "" && he.Code != http.StatusNotFound {
			t.Errorf("ref %q: kỳ vọng 404 (không tiết lộ), nhận %d", ref, he.Code)
		}
	}
}
