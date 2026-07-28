package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"
	goredis "github.com/redis/go-redis/v9"

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/integration/speech"
	"github.com/phonara/backend/internal/integration/storage"
	"github.com/phonara/backend/internal/integration/tts"
	"github.com/phonara/backend/internal/service"
	storedb "github.com/phonara/backend/internal/store/db"
	"github.com/phonara/backend/internal/worker"
)

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

	worker.RegisterHandlers(mux, pool, audioStore, engine, ttsProvider, enqueuer, gate)

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
