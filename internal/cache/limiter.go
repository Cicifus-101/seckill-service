package cache

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"math"
	"time"
)

// 区分“限流”和“系统错误”
var ErrRateLimited = errors.New("rate limited")

// RateLimiter 基于redis-lua的分布式令牌桶限流器
type RateLimiter struct {
	rdb *redis.Client
}

func NewRateLimiter(rdb *redis.Client) *RateLimiter {
	return &RateLimiter{
		rdb: rdb,
	}
}

var tokenBucketScript = redis.NewScript(`
local key      = KEYS[1]
local rate     = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local ttl      = tonumber(ARGV[3])
 
local time_pair = redis.call('TIME')  
local now = tonumber(time_pair[1]) * 1000000 + tonumber(time_pair[2])
 
local prev    = redis.call('HMGET', key, 'tokens', 'last')
local tokens_s = prev[1]
local last_s   = prev[2]
 
local tokens
local last
 
if tokens_s == false or tokens_s == nil then
	tokens = capacity
	last   = now
else
	tokens = tonumber(tokens_s)
	last   = tonumber(last_s)
	if tokens == nil or last == nil then
		tokens = capacity
		last   = now
	else
		local elapsed = (now - last) / 1000000.0
		if elapsed < 0 then elapsed = 0 end
		tokens = tokens + elapsed * rate
		if tokens > capacity then tokens = capacity end
		last = now
	end
end
 
local allowed = 0
if tokens >= 1.0 then
	tokens  = tokens - 1.0
	allowed = 1
end
 
redis.call('HSET', key,
	'tokens', string.format('%.12f', tokens),
	'last',   string.format('%.0f',  last))
if ttl > 0 then
	redis.call('EXPIRE', key, ttl)
end
 
return allowed
`)

// bucketParams 计算令牌桶参数
func bucketParams(limit int, burst int, window time.Duration) (rate, capacity float64, ttlSec int) {
	// burst:是短时间允许突发次数
	if limit <= 0 {
		limit = 0
	}
	if burst <= 0 {
		burst = limit
	}
	w := window.Seconds()
	if w <= 0 {
		w = 1
	}
	rate = float64(limit) / w
	capacity = float64(burst)
	ttlSec = int(math.Ceil(w * 3)) // 3倍窗口时间，防止key过期
	if ttlSec < 60 {
		ttlSec = 60
	}
	if ttlSec > 86400*7 {
		ttlSec = 86400 * 7
	}
	return rate, capacity, ttlSec
}

func (r *RateLimiter) runBucket(ctx context.Context, key string, rate, capacity float64, ttlSec int) (bool, error) {
	res, err := tokenBucketScript.Run(ctx, r.rdb, []string{key}, rate, capacity, ttlSec).Result()
	if err != nil {
		return false, fmt.Errorf("rate limiter redis error: %w", err)
	}
	allowed, ok := res.(int64)
	if !ok {
		return false, fmt.Errorf("rate limiter: unexpected script result type %T", res)
	}
	return allowed == 1, nil
}

// UserRateLimit 用户限流
func (r *RateLimiter) UserRateLimit(ctx context.Context, userID uint64, limit int, burst int, window time.Duration) (bool, error) {
	key := fmt.Sprintf("seckill:ratelimit:user:%d", userID)
	rate, capacity, ttl := bucketParams(limit, burst, window)

	return r.runBucket(ctx, key, rate, capacity, ttl)
}

// GlobalRateLimit 全局限流(所有实例共享同一 Redis key)
func (r *RateLimiter) GlobalRateLimit(ctx context.Context, limit int, burst int, window time.Duration) (bool, error) {
	key := "seckill:ratelimit:global:tokenbucket"
	rate, capacity, ttl := bucketParams(limit, burst, window)
	return r.runBucket(ctx, key, rate, capacity, ttl)
}

// ActivityRateLimit 单个秒杀活动限流
func (r *RateLimiter) ActivityRateLimit(ctx context.Context, activityID uint64, limit, burst int, window time.Duration) (bool, error) {
	key := fmt.Sprintf("seckill:ratelimit:activity:%d", activityID)
	rate, capacity, ttl := bucketParams(limit, burst, window)
	return r.runBucket(ctx, key, rate, capacity, ttl)
}
