package job

import (
	"context"
	"encoding/json"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"os"
	"seckill-service/internal/biz"
	"time"
)

type LeaderElector struct {
	cache biz.CacheRepo
	key   string
	ttl   time.Duration
	log   *log.Helper
}

// 在日志查询的时候知道leader的实例信息
type LeaderIdentity struct {
	Host string `json:"host"`
	PID  int    `json:"pid"`
	Key  string `json:"key"`
	ID   string `json:"id"`
}

func NewLeaderElector(cache biz.CacheRepo, key string, ttl time.Duration, logger log.Logger) *LeaderElector {
	return &LeaderElector{
		cache: cache,
		key:   key,
		ttl:   ttl,
		log:   log.NewHelper(log.With(logger, "module", "job/leader")),
	}
}

func (e *LeaderElector) Run(ctx context.Context, fn func(context.Context) error) error {
	retry := e.ttl / 3
	if retry < time.Second {
		retry = time.Second
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		token := e.newToken()
		acquired, err := e.cache.AcquireLock(ctx, e.key, token, e.ttl)
		if err != nil {
			e.log.WithContext(ctx).Warnf("leader acquire failed: key=%s err=%v", e.key, err)
			sleepOrDone(ctx, retry)
			continue
		}
		if !acquired {
			e.log.WithContext(ctx).Infof("skip non-leader execution: key=%s", e.key)
			sleepOrDone(ctx, retry)
			continue
		}

		e.log.WithContext(ctx).Infof("leader acquired: key=%s token=%s", e.key, token)
		_ = e.runAsLeader(ctx, token, fn)
	}
}

func (e *LeaderElector) runAsLeader(ctx context.Context, token string, fn func(context.Context) error) error {
	renewTicker := time.NewTicker(e.ttl / 3)
	defer renewTicker.Stop()
	defer func() { _ = e.cache.ReleaseLock(ctx, e.key, token) }()

	done := make(chan error, 1)
	go func() { done <- fn(ctx) }()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return err
		case <-renewTicker.C:
			ok, renewErr := e.cache.RenewLock(ctx, e.key, token, e.ttl)
			if renewErr != nil || !ok {
				e.log.WithContext(ctx).Warnf("leader lost: key=%s err=%v", e.key, renewErr)
				return nil
			}
		}
	}
}

func (e *LeaderElector) newToken() string {
	host, _ := os.Hostname()
	data, _ := json.Marshal(LeaderIdentity{
		Host: host,
		PID:  os.Getpid(),
		Key:  e.key,
		ID:   uuid.NewString(),
	})
	return string(data)
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
