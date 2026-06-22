package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/phonara/backend/internal/config"
	storedb "github.com/phonara/backend/internal/store/db"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

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

	slog.Info("running seed...")
	// TODO: implement seed runners for l1_error_tags, minimal_pairs, content_items, fix_guides
	slog.Info("seed complete")
}
