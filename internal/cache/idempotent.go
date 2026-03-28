package cache

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"time"
)

// idempotentChecker 幂等检查器
type IdempotentChecker struct {
	rdb *redis.Client
}

func NewIdempotentChecker(rdb *redis.Client) *IdempotentChecker {
	return &IdempotentChecker{
		rdb: rdb,
	}
}

// CheckAndMark 检查并标记请求
func (c *IdempotentChecker) CheckAndMark(ctx context.Context, requestId string, ttl time.Duration) (bool, error) {
	key := fmt.Sprintf("idempotent:%s", requestId)
	return c.rdb.SetNX(ctx, key, 1, ttl).Result()
}
