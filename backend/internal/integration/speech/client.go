// Package speech gọi pronunciation-engine (service Python nội bộ).
//
// Engine là hàm thuần: audio + text → kết quả. Nó không xác thực, không rate-limit,
// không chạm DB — Go backend là biên giới tin cậy duy nhất. Vì vậy client này KHÔNG
// gửi thông tin người dùng nào ngoài request_id để trace log.
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/phonara/backend/internal/domain"
)

// Client gọi engine qua HTTP.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient tạo client với timeout.
//
// Timeout phải lớn hơn thời gian inference chậm nhất chấp nhận được, nhưng đủ nhỏ để
// worker không treo khi engine chết. Inference đo được ~0,6–1,9 s cho câu 2,4 s; 30 s
// cho biên độ rộng kể cả khi engine đang xếp hàng.
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

// AssessInput là tham số một lần chấm.
type AssessInput struct {
	Audio         []byte
	ReferenceText string
	Locale        string
	RequestID     string
}

// Assess gửi audio + text tới engine.
//
// Trả về *domain.EngineError khi engine từ chối có lý do (mã lỗi phân loại được), hoặc
// lỗi thường khi mạng/parse hỏng. Caller dùng errors.As để phân biệt — quan trọng vì
// EngineError mang cờ Retryable mà worker cần.
func (c *Client) Assess(ctx context.Context, in AssessInput) (*domain.NormalizedAssessmentResult, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	fields := map[string]string{
		"reference_text": in.ReferenceText,
		"locale":         orDefault(in.Locale, "en-US"),
		"request_id":     in.RequestID,
	}
	for name, value := range fields {
		if err := mw.WriteField(name, value); err != nil {
			return nil, fmt.Errorf("write field %s: %w", name, err)
		}
	}
	part, err := mw.CreateFormFile("audio", "audio.wav")
	if err != nil {
		return nil, fmt.Errorf("create audio part: %w", err)
	}
	if _, err := part.Write(in.Audio); err != nil {
		return nil, fmt.Errorf("write audio: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.baseURL+"/v1/assess", &body,
	)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		// Mạng hỏng hoặc engine chết — tạm thời, đáng retry.
		return nil, &domain.EngineError{
			Code:      domain.EngErrInternal,
			Message:   fmt.Sprintf("gọi engine thất bại: %v", err),
			Retryable: true,
		}
	}
	defer resp.Body.Close() //nolint:errcheck

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var engErr domain.EngineError
		if err := json.Unmarshal(payload, &engErr); err != nil || engErr.Code == "" {
			// Engine trả lỗi không đúng định dạng — coi là tạm thời, nhưng nói rõ
			// trong message để không che mất sự cố thật.
			return nil, &domain.EngineError{
				Code:      domain.EngErrInternal,
				Message:   fmt.Sprintf("engine trả HTTP %d không đúng định dạng: %s", resp.StatusCode, truncate(payload, 200)),
				Retryable: true,
			}
		}
		return nil, &engErr
	}

	var result domain.NormalizedAssessmentResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("parse kết quả engine: %w", err)
	}
	if result.Engine == "" || result.ModelVersion == "" {
		// Thiếu dấu vết phiên bản thì kết quả không truy vết được về sau — từ chối
		// ngay thay vì lưu một bản ghi mồ côi.
		return nil, fmt.Errorf("kết quả engine thiếu engine/model_version")
	}
	return &result, nil
}

// Health kiểm tra engine sẵn sàng chưa (model đã nạp VÀ warm-up xong).
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("engine chưa sẵn sàng: HTTP %d", resp.StatusCode)
	}
	return nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
