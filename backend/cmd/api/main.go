package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"

	"github.com/hibiken/asynq"
	"github.com/phonara/backend/internal/config"

	"github.com/phonara/backend/internal/integration/storage"
	jwtutil "github.com/phonara/backend/internal/pkg/jwt"
	"github.com/phonara/backend/internal/server"
	"github.com/phonara/backend/internal/service"
	storedb "github.com/phonara/backend/internal/store/db"
	storeredis "github.com/phonara/backend/internal/store/redis"
)

func main() {
	// Structured logging (text in dev, JSON in prod)
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	var logHandler slog.Handler
	if cfg.App.Env == "production" {
		logHandler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		logHandler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(logHandler))

	ctx := context.Background()

	// Database pool
	pool, err := storedb.NewPool(ctx, cfg.DB)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("database connected", "host", cfg.DB.Host, "db", cfg.DB.Name)

	// Redis
	rdb, err := storeredis.NewClient(ctx, cfg.Redis)
	if err != nil {
		slog.Error("connect redis", "err", err)
		os.Exit(1)
	}
	slog.Info("redis connected", "addr", cfg.Redis.Addr)

	// JWT manager
	jwtMgr := jwtutil.New(
		cfg.JWT.AccessSecret,
		cfg.JWT.RefreshSecret,
		cfg.JWT.AccessTTL,
		cfg.JWT.RefreshTTL,
	)

	// Server
	audioStore, err := storage.New(ctx, storage.FactoryConfig{
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

	enqueue := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer enqueue.Close()

	// Inspector đọc độ sâu hàng đợi để gate từ chối sớm khi engine quá tải.
	inspector := asynq.NewInspector(asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer inspector.Close()

	gate := service.NewPracticeGate(pool, rdb, cfg, inspector)

	srv := server.New(cfg, pool, rdb, jwtMgr, audioStore, enqueue, gate)

	// Register validator
	srv.Echo().Validator = &echoValidator{validate: validator.New()}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("starting server", "addr", cfg.Server.Addr())
		if err := srv.Start(); err != nil {
			slog.Info("server stopped", "err", err)
		}
	}()

	<-quit
	slog.Info("shutting down server...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("server shutdown", "err", err)
	}

	slog.Info("server exited")
}

// echoValidator wraps go-playground/validator for Echo.
type echoValidator struct {
	validate *validator.Validate
}

func (v *echoValidator) Validate(i any) error {
	return v.validate.Struct(i)
}

var _ echo.Validator = (*echoValidator)(nil)
