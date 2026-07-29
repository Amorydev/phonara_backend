package worker

import (
	"context"
	"errors"
	"time"
)

// ErrEngineBusy nghĩa là không giành được suất inference trong thời gian cho phép.
//
// Cố ý KHÔNG bọc asynq.SkipRetry: job vẫn đáng làm, chỉ là chưa tới lượt. Nó quay lại qua
// RetryDelayFunc riêng ở cmd/worker (vài giây), không phải qua mặc định của Asynq
// (15–44 giây cho lần retry đầu).
var ErrEngineBusy = errors.New("engine đang bận, chưa giành được suất inference")

// EngineGate giới hạn số lượt inference chạy song song, khớp với engine.
//
// ═══ VÌ SAO CẦN ═══
//
// pronunciation-engine chỉ nhận `PE_MAX_CONCURRENT_INFERENCE` lượt cùng lúc (production:
// 1). Quá số đó nó trả 503 `model_overloaded` — mã retryable, nên Asynq đẩy job vào hàng
// đợi retry với `DefaultRetryDelayFunc` = `n⁴ + 15 + rand(30)` giây. Lần retry ĐẦU TIÊN
// đã là 15–44 giây.
//
// Mà `ASYNQ_CONCURRENCY` mặc định là 10. Mười worker bắn mười job, engine nhận một, chín
// job còn lại ngủ 15–44 giây. Một lượt chấm 4,5 giây biến thành gần một phút, và log chỉ
// nói "engine chậm".
//
// Chặn ở đây thì job xếp hàng TRONG tiến trình worker — chi phí vài micro giây, thứ tự
// công bằng theo kênh, và engine không bao giờ phải từ chối ai.
//
// ═══ VÌ SAO CÓ HẠN CHỜ ═══
//
// Asynq dùng CHUNG một pool goroutine cho mọi loại task. Chờ vô hạn ở đây nghĩa là đủ job
// chấm phát âm sẽ chiếm sạch pool và làm đứng TTS, thông báo, mọi thứ khác. Quá hạn thì
// trả ErrEngineBusy và nhường chỗ.
type EngineGate struct {
	slots chan struct{}
	wait  time.Duration
}

// NewEngineGate → nil nếu concurrency < 1, và cổng nil cho qua tất cả. Điều đó giữ cho
// test và các đường gọi cũ không phải biết tới cổng này.
func NewEngineGate(concurrency int, wait time.Duration) *EngineGate {
	if concurrency < 1 {
		return nil
	}
	if wait <= 0 {
		wait = 45 * time.Second
	}
	return &EngineGate{slots: make(chan struct{}, concurrency), wait: wait}
}

// Acquire giữ một suất, hoặc trả lỗi. Gọi Release đúng một lần khi Acquire trả nil.
func (g *EngineGate) Acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}
	timer := time.NewTimer(g.wait)
	defer timer.Stop()

	select {
	case g.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		// Task bị huỷ (Asynq shutdown hoặc hết hạn task) — trả nguyên lỗi ngữ cảnh để
		// tầng trên phân biệt được với "bận".
		return ctx.Err()
	case <-timer.C:
		return ErrEngineBusy
	}
}

// Release trả suất. An toàn khi gọi trên cổng nil.
func (g *EngineGate) Release() {
	if g == nil {
		return
	}
	select {
	case <-g.slots:
	default:
		// Release không cặp với Acquire là lỗi lập trình, nhưng chặn ở đây sẽ treo worker
		// vĩnh viễn. Bỏ qua thì cùng lắm là nới lỏng giới hạn, không làm chết tiến trình.
	}
}
