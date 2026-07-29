package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEngineGateSerialisesInference(t *testing.T) {
	t.Parallel()

	// Đây là tính chất chính: engine chỉ chấm được một câu tại một thời điểm, nên cổng
	// phải bảo đảm không bao giờ có hai lượt cùng chạy. Thiếu nó thì lượt thứ hai bị
	// engine trả 503 và rơi vào retry 15–44 giây của Asynq.
	gate := NewEngineGate(1, time.Second)

	var running, maxRunning atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := gate.Acquire(context.Background()); err != nil {
				return
			}
			defer gate.Release()

			now := running.Add(1)
			for {
				peak := maxRunning.Load()
				if now <= peak || maxRunning.CompareAndSwap(peak, now) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
		}()
	}
	wg.Wait()

	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("có %d lượt chạy song song, cổng phải giữ đúng 1", got)
	}
}

func TestEngineGateAllowsConfiguredParallelism(t *testing.T) {
	t.Parallel()

	gate := NewEngineGate(3, time.Second)
	for i := range 3 {
		if err := gate.Acquire(context.Background()); err != nil {
			t.Fatalf("suất %d bị từ chối: %v", i, err)
		}
	}
	// Suất thứ tư phải chờ rồi quá hạn.
	gate.wait = 20 * time.Millisecond
	if err := gate.Acquire(context.Background()); !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("suất thứ tư: muốn ErrEngineBusy, nhận %v", err)
	}
}

func TestEngineGateTimesOutInsteadOfBlockingForever(t *testing.T) {
	t.Parallel()

	// Asynq dùng chung pool goroutine cho MỌI loại task. Chờ vô hạn ở đây nghĩa là đủ
	// job chấm phát âm sẽ làm đứng cả TTS lẫn thông báo.
	gate := NewEngineGate(1, 30*time.Millisecond)
	if err := gate.Acquire(context.Background()); err != nil {
		t.Fatalf("suất đầu bị từ chối: %v", err)
	}

	start := time.Now()
	err := gate.Acquire(context.Background())
	elapsed := time.Since(start)

	if !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("muốn ErrEngineBusy, nhận %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("chờ %v — hạn 30ms không có tác dụng", elapsed)
	}
}

func TestEngineGateHonoursContextCancellation(t *testing.T) {
	t.Parallel()

	// Worker tắt giữa chừng phải thoát ngay, không nằm chờ hết hạn. Và lỗi trả về phải
	// là lỗi ngữ cảnh chứ không phải ErrEngineBusy — hai thứ này dẫn tới hai cách xử lý
	// khác nhau ở tầng trên.
	gate := NewEngineGate(1, time.Hour)
	if err := gate.Acquire(context.Background()); err != nil {
		t.Fatalf("suất đầu bị từ chối: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := gate.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("muốn context.Canceled, nhận %v", err)
	}
	if errors.Is(err, ErrEngineBusy) {
		t.Fatal("huỷ ngữ cảnh không được báo thành ErrEngineBusy")
	}
}

func TestNilEngineGateLetsEverythingThrough(t *testing.T) {
	t.Parallel()

	// Cổng nil = không giới hạn. Giữ cho các đường gọi cũ và test không phải biết tới nó.
	var gate *EngineGate
	if err := gate.Acquire(context.Background()); err != nil {
		t.Fatalf("cổng nil phải cho qua, nhận %v", err)
	}
	gate.Release() // không được panic
}

func TestEngineGateReleaseWithoutAcquireDoesNotDeadlock(t *testing.T) {
	t.Parallel()

	// Release thừa là lỗi lập trình, nhưng nếu nó chặn thì worker treo vĩnh viễn — hậu
	// quả tệ hơn nhiều so với việc nới lỏng giới hạn một nhịp.
	gate := NewEngineGate(1, time.Millisecond)
	done := make(chan struct{})
	go func() {
		gate.Release()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Release không cặp với Acquire đã làm treo")
	}
}
