// Package redis provides the Redis client.
package redis

import (
	"context"
	"fmt"

	"github.com/phonara/backend/internal/config"
	goredis "github.com/redis/go-redis/v9"
)

// NewClient creates and pings a Redis client.
func NewClient(ctx context.Context, cfg config.RedisConfig) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}
