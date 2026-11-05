package datastore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chriswood123/zealot/internal/config"
	"github.com/redis/go-redis/v9"
)

// redisStore is a pooled Redis connection (go-redis pools internally).
type redisStore struct {
	c *redis.Client
}

func newRedis(ctx context.Context, cfg config.Datastore) (Datastore, error) {
	c := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &redisStore{c: c}, nil
}

// Exec runs one Redis command. GET misses (redis.Nil) return nil: a cache
// miss is expected behaviour under load, not a pipeline failure.
func (r *redisStore) Exec(ctx context.Context, q *config.QueryStep) error {
	op := strings.ToUpper(q.Op)
	if q.Key == "" {
		return fmt.Errorf("redis %s: key is required", op)
	}
	switch op {
	case "GET":
		_, err := r.c.Get(ctx, q.Key).Result()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	case "SET":
		return r.c.Set(ctx, q.Key, q.Value, 0).Err()
	case "DEL":
		return r.c.Del(ctx, q.Key).Err()
	default:
		return fmt.Errorf("redis: unsupported op %q (want GET, SET or DEL)", q.Op)
	}
}

func (r *redisStore) Close() error { return r.c.Close() }
