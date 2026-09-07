package cache

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

var ErrRedisCircuitOpen = errors.New("redis circuit is open")

// RedisGuard protects the critical seckill write path from repeatedly
// hitting an unavailable Redis instance. It deliberately fails closed:
// callers must not fall back to MySQL for stock deduction.
type RedisGuard struct {
	rdb           *redis.Client
	mu            sync.Mutex
	failures      int
	threshold     int
	openUntil     time.Time
	openTimeout   time.Duration
	probeInFlight bool
}

func NewRedisGuard(rdb *redis.Client) *RedisGuard {
	return &RedisGuard{
		rdb:         rdb,
		threshold:   3,
		openTimeout: 5 * time.Second,
	}
}

// Allow performs a short health probe while the circuit is closed or half-open.
// A successful probe closes the circuit; a failed probe opens it after the
// threshold is reached.
func (g *RedisGuard) Allow(ctx context.Context) error {
	g.mu.Lock()
	now := time.Now()
	if now.Before(g.openUntil) {
		g.mu.Unlock()
		return ErrRedisCircuitOpen
	}
	if !g.openUntil.IsZero() {
		if g.probeInFlight {
			g.mu.Unlock()
			return ErrRedisCircuitOpen
		}
		g.probeInFlight = true
	}
	g.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	err := g.rdb.Ping(probeCtx).Err()

	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil {
		g.failures = 0
		g.openUntil = time.Time{}
		g.probeInFlight = false
		return nil
	}

	g.probeInFlight = false
	g.failures++
	if g.failures >= g.threshold {
		g.openUntil = time.Now().Add(g.openTimeout)
	}
	return err
}

// RecordFailure lets the caller trip the circuit when a critical Redis
// operation fails after the initial health probe.
func (g *RedisGuard) RecordFailure() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures++
	if g.failures >= g.threshold {
		g.openUntil = time.Now().Add(g.openTimeout)
	}
}

func (g *RedisGuard) RecordSuccess() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.openUntil.IsZero() {
		g.failures = 0
	}
}

func (g *RedisGuard) IsOpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().Before(g.openUntil)
}

var _ interface {
	Allow(context.Context) error
	RecordFailure()
	RecordSuccess()
} = (*RedisGuard)(nil)
