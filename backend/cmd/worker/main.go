package main

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/domain"
	"github.com/phonara/backend/internal/integration/speech"
	"github.com/phonara/backend/internal/integration/storage"
	"github.com/phonara/backend/internal/integration/tts"
	"github.com/phonara/backend/internal/service"
	storedb "github.com/phonara/backend/internal/store/db"
	"github.com/phonara/backend/internal/worker"
)

// backpressureRetryDelay tách "hệ thống đang bận" khỏi "job này hỏng".
//
// Mặc định của Asynq là `n⁴ + 15 + rand(30)` giây — lần retry ĐẦU TIÊN đã chờ 15–44 giây.
// Con số đó hợp lý cho lỗi thật (API ngoài sập, DB mất kết nối): lùi lại thật xa, đừng dội
// thêm vào thứ đang hỏng.
//
// Nhưng nó SAI hoàn toàn cho áp lực ngược. Engine chỉ chấm được một câu tại một thời điểm;
// job thứ hai không hỏng gì cả, nó chỉ đến sớm vài giây. Bắt nó ngủ 15–44 giây biến một
// lượt chấm 4,5 giây thành gần một phút, và người học ngồi nhìn màn hình chờ.
//
// Hai trường hợp được rút ngắn:
//
//	ErrEngineBusy          — worker tự chặn ở EngineGate (đường đi bình thường)
//	EngErrModelOverloaded  — engine trả 503 vì đã có lượt khác chạy. Đường này chỉ xảy ra
//	                         khi PRONUNCIATION_ENGINE_CONCURRENCY đặt CAO HƠN
//	                         PE_MAX_CONCURRENT_INFERENCE. Rút ngắn ở đây là lưới an toàn
//	                         cho cấu hình lệch, không phải đường đi thiết kế.
func backpressureRetryDelay(n int, err error, task *asynq.Task) time.Duration {
	overloaded := errors.Is(err, worker.ErrEngineBusy)
	var engErr *domain.EngineError
	if errors.As(err, &engErr) && engErr.Code == domain.EngErrModelOverloaded {
		overloaded = true
	}
	if !overloaded {
		return asynq.DefaultRetryDelayFunc(n, err, task)
	}
	// Lùi nhẹ và có nhiễu: nhiều job cùng bị chặn phải quay lại lệch nhau, nếu không
	// chúng sẽ đồng loạt dội vào engine cùng một khoảnh khắc rồi cùng bị chặn tiếp.
	base := time.Duration(1+n) * time.Second
	if base > 10*time.Second {
		base = 10 * time.Second
	}
	return base + time.Duration(rand.IntN(1000))*time.Millisecond
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	pool, err := storedb.NewPool(ctx, cfg.DB)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	redisOpt := asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}

	var audioStore storage.Store
	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.Asynq.Concurrency,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
		RetryDelayFunc: asynq.RetryDelayFunc(backpressureRetryDelay),
		// Không có ErrorHandler thì task lỗi bị NUỐT HOÀN TOÀN: asynq retry âm thầm,
		// job nằm mãi ở trạng thái processing, client poll mãi không có kết quả, và
		// log không ghi một dòng nào. Đây là lỗi vận hành tệ nhất — hỏng mà im lặng.
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			retried, _ := asynq.GetRetryCount(ctx)
			maxRetry, _ := asynq.GetMaxRetry(ctx)
			worker.HandleTaskError(ctx, pool, audioStore, t)
			slog.Error("task thất bại",
				"type", t.Type(),
				"payload", string(t.Payload()),
				"retry", retried,
				"max_retry", maxRetry,
				"giving_up", retried >= maxRetry,
				"err", err,
			)
		}),
	})

	mux := asynq.NewServeMux()
	audioStore, err = storage.New(ctx, storage.FactoryConfig{
		Driver:          cfg.S3.Driver,
		LocalRoot:       cfg.Storage.LocalRoot,
		Endpoint:        cfg.S3.Endpoint,
		AccessKey:       cfg.S3.AccessKey,
		SecretKey:       cfg.S3.SecretKey,
		Region:          cfg.S3.Region,
		SampleBucket:    cfg.S3.SampleBucket,
		RecordingBucket: cfg.S3.RecordingBucket,
	})
	if err != nil {
		slog.Error("init audio storage", "err", err)
		os.Exit(1)
	}
	engine := speech.NewClient(cfg.Engine.URL, cfg.Engine.Timeout)

	// Chưa có key → NoopProvider, và worker sẽ để sample_audio_url RỖNG thay vì bịa URL.
	var ttsProvider tts.Provider = tts.NoopProvider{}
	if cfg.Azure.TTSKey != "" && cfg.Azure.TTSRegion != "" {
		ttsProvider = tts.NewAzure(cfg.Azure.TTSKey, cfg.Azure.TTSRegion, cfg.Azure.TTSVoice)
	}
	slog.Info("tts provider", "name", ttsProvider.Name())

	// Worker cần client riêng để tự đẩy lô tiếp cho tác vụ chạy nhiều lô.
	enqueuer := asynq.NewClient(redisOpt)
	defer enqueuer.Close()

	inspector := asynq.NewInspector(redisOpt)
	defer inspector.Close()

	// Worker cần Redis riêng để hoàn lượt quota khi bản ghi không dùng được.
	rdb := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer rdb.Close()

	gate := service.NewPracticeGate(pool, rdb, cfg, inspector)

	engineGate := worker.NewEngineGate(cfg.Engine.Concurrency, cfg.Engine.AcquireTimeout)
	worker.RegisterHandlers(mux, pool, audioStore, engine, ttsProvider, enqueuer, gate, engineGate)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("starting worker", "concurrency", cfg.Asynq.Concurrency)
		if err := srv.Run(mux); err != nil {
			slog.Error("worker error", "err", err)
		}
	}()

	<-quit
	slog.Info("shutting down worker...")
	srv.Shutdown()
}
