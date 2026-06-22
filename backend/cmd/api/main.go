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

	"github.com/phonara/backend/internal/config"
	"github.com/phonara/backend/internal/server"
	storedb "github.com/phonara/backend/internal/store/db"
	storeredis "github.com/phonara/backend/internal/store/redis"
	jwtutil "github.com/phonara/backend/internal/pkg/jwt"
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
	srv := server.New(cfg, pool, rdb, jwtMgr)

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
