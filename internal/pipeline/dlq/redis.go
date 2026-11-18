package dlq

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// RedisDedup releases deduplication keys so archived signals can be
// re-ingested by the connectors on their next poll.
type RedisDedup struct {
	rdb redis.UniversalClient
}

// NewRedisDedup creates a DedupStore backed by a go-redis client.
func NewRedisDedup(rdb redis.UniversalClient) (*RedisDedup, error) {
	if rdb == nil {
		return nil, errors.New("dlq: redis client is required")
	}
	return &RedisDedup{rdb: rdb}, nil
}

func (r *RedisDedup) DeleteKey(ctx context.Context, key string) error {
	return r.rdb.Del(ctx, key).Err()
}
