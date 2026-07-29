package main

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/worker"
)

// Mặc định của Asynq cho lần retry đầu là `0⁴ + 15 + rand(30)` = 15–44 giây. Đây chính là
// con số biến một lượt chấm 4,5 giây thành gần một phút khi có hai người tập cùng lúc.
const asynqDefaultFloor = 15 * time.Second

func TestBackpressureRetryIsFastForEngineBusy(t *testing.T) {
	t.Parallel()

	// Bọc lỗi để giống hệt đường đi thật: handler trả `fmt.Errorf("chờ suất inference: %w", err)`.
	err := fmt.Errorf("chờ suất inference: %w", worker.ErrEngineBusy)

	for attempt := range 3 {
		got := backpressureRetryDelay(attempt, err, nil)
		if got >= asynqDefaultFloor {
			t.Fatalf("lần %d: chờ %v — phải ngắn hơn mặc định %v của Asynq",
				attempt, got, asynqDefaultFloor)
		}
	}
}

func TestBackpressureRetryIsFastForModelOverloaded(t *testing.T) {
	t.Parallel()

	// Đường này chỉ xảy ra khi PRONUNCIATION_ENGINE_CONCURRENCY đặt cao hơn
	// PE_MAX_CONCURRENT_INFERENCE. Là lưới an toàn cho cấu hình lệch.
	err := fmt.Errorf("gọi engine: %w", &domain.EngineError{
		Code:      domain.EngErrModelOverloaded,
		Message:   "quá 1 inference đồng thời",
		Retryable: true,
	})

	if got := backpressureRetryDelay(0, err, nil); got >= asynqDefaultFloor {
		t.Fatalf("chờ %v — 503 quá tải phải quay lại nhanh", got)
	}
}

func TestBackpressureRetryKeepsDefaultForRealFailures(t *testing.T) {
	t.Parallel()

	// Lỗi thật (engine sập, DB mất kết nối) PHẢI giữ backoff dài. Rút ngắn cho mọi lỗi
	// sẽ biến worker thành cỗ máy dội request vào đúng thứ đang hỏng.
	cases := map[string]error{
		"lỗi thường":               errors.New("connection refused"),
		"lỗi engine không quá tải": &domain.EngineError{Code: domain.EngErrInternal, Retryable: true},
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := backpressureRetryDelay(0, err, nil)
			want := asynq.DefaultRetryDelayFunc(0, err, nil)
			// DefaultRetryDelayFunc có nhiễu ngẫu nhiên nên chỉ so được cận dưới.
			if got < asynqDefaultFloor {
				t.Fatalf("chờ %v (mặc định quanh %v) — lỗi thật không được rút ngắn", got, want)
			}
		})
	}
}

func TestBackpressureRetryBacksOffAcrossAttempts(t *testing.T) {
	t.Parallel()

	// Nhiều job cùng bị chặn phải quay lại lệch nhau và thưa dần, nếu không chúng đồng
	// loạt dội vào engine cùng một khoảnh khắc rồi cùng bị chặn tiếp.
	err := worker.ErrEngineBusy
	first := backpressureRetryDelay(0, err, nil)
	later := backpressureRetryDelay(5, err, nil)
	if later <= first {
		t.Fatalf("lần 5 (%v) phải lâu hơn lần 0 (%v)", later, first)
	}
	if capped := backpressureRetryDelay(100, err, nil); capped > 12*time.Second {
		t.Fatalf("chờ %v — phải có trần, không được tăng vô hạn", capped)
	}
}
