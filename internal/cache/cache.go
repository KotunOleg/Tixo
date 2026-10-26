package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

type Store struct {
	rdb *redis.Client
	log *slog.Logger
}

func New(rdb *redis.Client, log *slog.Logger) *Store {
	return &Store{rdb: rdb, log: log}
}

func Fetch[T any](ctx context.Context, s *Store, ns, id string, ttl time.Duration, load func(context.Context) (T, error)) (T, bool, error) {
	key, ok := s.key(ctx, ns, id)
	if ok {
		if raw, err := s.rdb.Get(ctx, key).Bytes(); err == nil {
			var val T
			if err := json.Unmarshal(raw, &val); err == nil {
				return val, true, nil
			}
		} else if !errors.Is(err, redis.Nil) {
			s.log.WarnContext(ctx, "cache get failed", "error", err, "key", key)
		}
	}

	val, err := load(ctx)
	if err != nil {
		var zero T
		return zero, false, err
	}

	if ok {
		if raw, err := json.Marshal(val); err == nil {
			if err := s.rdb.Set(ctx, key, raw, ttl).Err(); err != nil {
				s.log.WarnContext(ctx, "cache set failed", "error", err, "key", key)
			}
		}
	}
	return val, false, nil
}

func (s *Store) Bump(ctx context.Context, ns string) {
	if err := s.rdb.Incr(ctx, ns+":ver").Err(); err != nil {
		s.log.WarnContext(ctx, "cache bump failed", "error", err, "ns", ns)
	}
}

func (s *Store) key(ctx context.Context, ns, id string) (string, bool) {
	ver, err := s.rdb.Get(ctx, ns+":ver").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ns + ":v0:" + id, true
		}
		s.log.WarnContext(ctx, "cache read version failed", "error", err, "ns", ns)
		return "", false
	}
	return ns + ":v" + ver + ":" + id, true
}
