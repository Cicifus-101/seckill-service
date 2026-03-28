package cache

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"time"
)

// RateLimiter 限流器
type RateLimiter struct {
	rdb *redis.Client
}

func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{
		rdb: rdb,
	}
}

const (
	script = `
    local current = redis.call('incr', KEYS[1])
    if current == 1 then
        redis.call('expire', KEYS[1], ARGV[1])
    end
    if current > tonumber(ARGV[2]) then
        return 0
    end
    return 1`
)

// UserRateLimit 用户限流
func (r *RateLimiter) UserRateLimit(ctx context.Context, userID uint64, limit int, window time.Duration) (bool, error) {
	key := fmt.Sprintf("seckill:user:%d", userID)
	res, err := r.rdb.Eval(ctx, script, []string{key}, int(window.Seconds()), limit).Result()
	if err != nil {
		return false, err
	}
	return res.(int64) == 1, nil
}

// GlobalRateLimit 全局限流
func (r *RateLimiter) GlobalRateLimit(ctx context.Context, limit int, window time.Duration) (bool, error) {
	key := "ratelimit:global:seckill"
	res, err := r.rdb.Eval(ctx, script, []string{key}, int(window.Seconds()), limit).Result()
	if err != nil {
		return false, err
	}
	return res.(int64) == 1, nil
}
