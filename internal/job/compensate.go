package job

import (
	"context"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"seckill-service/internal/biz"
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
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.log.Info("补偿任务停止")
			return
		case <-ticker.C:
			t.syncStockConsistency(ctx)
		}
	}
}

// Start 启动补偿
func (t *CompensateTask) syncStockConsistency(ctx context.Context) {
	products, _, err := t.mysql.ListSeckillProducts(ctx, 0, 1, 1000, 0)
	if err != nil {
		t.log.Errorf("获取商品列表失败: %v", err)
		return
	}
	for _, p := range products {
		redisStock, err := t.cache.GetStock(ctx, p.SkuID)
		if err != nil {
			t.log.Errorf("获取 Redis 库存失败: sku=%d, err=%v", p.SkuID, err)
			continue
		}

		//让redis和mysql中的库存保持一致
		if redisStock != p.AvailableStock {
			t.log.Warnf("库存不一致: sku=%d, redis=%d, mysql=%d", p.SkuID, redisStock, p.AvailableStock)
			if err := t.cache.SetStock(ctx, p.SkuID, p.AvailableStock); err != nil {
				t.log.Errorf("同步 Redis 库存失败: sku=%d, err=%v", p.SkuID, err)
			}
		}
	}
}
