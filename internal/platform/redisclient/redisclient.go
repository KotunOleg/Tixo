package redisclient

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/KotunOleg/Tixo/internal/health"
)

func New(url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse REDIS_URL: %w", err)
	}
	return redis.NewClient(opts), nil
}

func Checker(client *redis.Client) health.Checker {
	return health.CheckFunc{
		CheckerName: "redis",
		Fn: func(ctx context.Context) error {
			return client.Ping(ctx).Err()
		},
	}
}
