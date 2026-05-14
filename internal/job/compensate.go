package job

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"seckill-service/internal/biz"
	"seckill-service/internal/observability"
	"time"
)

// CompensateTask 补偿任务
type CompensateTask struct {
	rdb   *redis.Client
	mysql biz.SeckillRepo
	cache biz.CacheRepo
	log   *log.Helper
}

func NewCompensateTask(rdb *redis.Client, mysql biz.SeckillRepo, cache biz.CacheRepo, logger log.Logger) *CompensateTask {
	return &CompensateTask{
		rdb:   rdb,
		mysql: mysql,
		cache: cache,
		log:   log.NewHelper(log.With(logger, "module", "job/compensate")),
	}
}

// Start 启动扫描
func (t *CompensateTask) Start(ctx context.Context) {
	elector := NewLeaderElector(t.cache, "seckill:leader:compensate", 30*time.Second, t.log.Logger())
	_ = elector.Run(ctx, func(runCtx context.Context) error {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-runCtx.Done():
				t.log.Info("compensate task stopped")
				return nil
			case <-ticker.C:
				t.syncStockConsistency(runCtx)
				t.fixPendingReservations(runCtx)
			}
		}
	})
}

func (t *CompensateTask) syncStockConsistency(ctx context.Context) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "job.CompensateTask.syncStockConsistency")
	defer func() {
		observability.Finish(span, nil)
		observability.ObserveOperation("job", "compensate_sync", "success", start)
	}()

	products, _, err := t.mysql.ListSeckillProducts(ctx, 0, 1, 1000, 0)
	if err != nil {
		t.log.Errorf("获取商品列表失败: %v", err)
		return
	}
	for _, p := range products {
		redisStock, err := t.cache.GetStock(ctx, p.ActivityID, p.SkuID)
		if err != nil {
			t.log.Errorf("获取 Redis 库存失败: sku=%d, err=%v", p.SkuID, err)
			continue
		}

		//让redis和mysql中的库存保持一致
		if redisStock != p.AvailableStock {
			t.log.Warnf("库存不一致: sku=%d, redis=%d, mysql=%d", p.SkuID, redisStock, p.AvailableStock)
			if err := t.cache.SetStock(ctx, p.ActivityID, p.SkuID, p.AvailableStock); err != nil {
				t.log.Errorf("同步 Redis 库存失败: sku=%d, err=%v", p.SkuID, err)
			}
		}
	}
}

func (t *CompensateTask) fixPendingReservations(ctx context.Context) {
	iter := t.rdb.Scan(ctx, 0, "seckill:pending:*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()

		data, err := t.rdb.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}

		var pending biz.PendingReservation
		if err := json.Unmarshal(data, &pending); err != nil {
			_ = t.rdb.Del(ctx, key).Err()
			continue
		}

		if time.Since(time.Unix(pending.CreatedAt, 0)) < 2*time.Minute {
			continue
		}

		_, err = t.mysql.GetOrderByRequestID(ctx, pending.RequestID)
		if err == nil {
			_ = t.rdb.Del(ctx, key).Err()
			continue
		}

		if !errors.Is(err, biz.ErrOrderNotFound) {
			t.log.Warnf("pending order check failed: requestID=%s err=%v", pending.RequestID, err)
			continue
		}

		if err := t.cache.RollbackStock(ctx, pending.ActivityID, pending.SkuID, pending.Quantity); err != nil {
			t.log.Warnf("pending rollback stock failed: requestID=%s err=%v", pending.RequestID, err)
			continue
		}

		if err := t.cache.RemoveUserBuy(ctx, pending.ActivityID, pending.SkuID, pending.UserID); err != nil {
			t.log.Warnf("pending remove buy mark failed: requestID=%s err=%v", pending.RequestID, err)
			continue
		}

		if pending.CouponID > 0 {
			if err := t.mysql.RestoreUserCoupon(ctx, pending.CouponID); err != nil {
				t.log.Warnf("pending restore user coupon failed: requestID=%s couponID=%d err=%v", pending.RequestID, pending.CouponID, err)
				continue
			}
			_ = t.cache.DeleteCoupon(ctx, pending.CouponID)
		}

		_ = t.rdb.Del(ctx, key).Err()
		t.log.Warnf("pending reservation compensated: requestID=%s orderNo=%s", pending.RequestID, pending.OrderNo)
	}

	if err := iter.Err(); err != nil {
		t.log.Warnf("scan pending reservations failed: %v", err)
	}
}
