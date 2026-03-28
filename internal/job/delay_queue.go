package job

import (
	"context"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"seckill-service/internal/biz"
	"time"
)

// DelayQueue 延迟队列
type DelayQueue struct {
	rdb   *redis.Client
	mysql biz.SeckillRepo
	cache biz.CacheRepo
	log   *log.Helper
}

func NewDelayQueue(rdb *redis.Client, mysql biz.SeckillRepo, cache biz.CacheRepo, logger log.Logger) *DelayQueue {
	return &DelayQueue{
		rdb:   rdb,
		mysql: mysql,
		cache: cache,
		log:   log.NewHelper(log.With(logger, "module", "job/delay")),
	}
}

// Add 添加延迟任务
func (q *DelayQueue) Add(ctx context.Context, orderNo string, delay time.Duration) error {
	score := float64(time.Now().Add(delay).Unix()) // 将时间转换为时间戳
	return q.rdb.ZAdd(ctx, "delay:orders", &redis.Z{Score: score, Member: orderNo}).Err()
}

// Start 启动扫描
func (q *DelayQueue) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.log.Info("延迟队列停止")
			return
		case <-ticker.C:
			q.scan(ctx)
		}
	}
}

// 内部方法
func (q *DelayQueue) scan(ctx context.Context) {
	now := float64(time.Now().Unix())

	orders, err := q.rdb.ZRangeByScore(ctx, "delay:orders", &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%f", now),
	}).Result()

	if err != nil || len(orders) == 0 {
		return
	}

	for _, orderNo := range orders {
		script := `
        local removed = redis.call('zrem', KEYS[1], ARGV[1])
        if removed == 1 then return 1 end
        return 0
        `
		res, _ := q.rdb.Eval(ctx, script, []string{"delay:orders"}).Int()
		if res == 1 {
			q.handleTimeout(ctx, orderNo)
		}
	}
}

func (q *DelayQueue) handleTimeout(ctx context.Context, orderNo string) {
	lockKey := fmt.Sprintf("lock:order:%s", orderNo)
	lockValue := uuid.New().String()
	lock, err := q.rdb.SetNX(ctx, lockKey, lockValue, 30*time.Second).Result()
	if err != nil || !lock {
		return
	}

	// 检验之后再删除，防止误删他人锁
	defer func() {
		script := `
            if redis.call("get", KEYS[1]) == ARGV[1] then
                return redis.call("del", KEYS[1])
            else
                return 0
            end`
		q.rdb.Eval(ctx, script, []string{lockKey}, lockKey)
	}()

	// 更新Mysql数据库
	order, err := q.mysql.GetOrderForUpdate(ctx, orderNo)
	if err != nil {
		q.log.Errorf("获取订单失败: %v", err)
		return
	}

	if order.Status != biz.OrderStatusPending {
		return
	}

	// 更新订单状态
	if err := q.mysql.UpdateOrderStatus(ctx, orderNo, biz.OrderStatusCancel); err != nil {
		q.log.Errorf("更新订单状态失败: %v", err)
		return
	}

	// 恢复 MySQL 库存
	if err := q.mysql.RestoreStock(ctx, order.SkuID, uint32(order.Quantity)); err != nil {
		q.log.Errorf("恢复 MySQL 库存失败: %v", err)
	}

	// 恢复 redis 库存
	q.cache.RollbackStock(ctx, order.SkuID, int(order.Quantity))
	q.cache.RemoveUserBuy(ctx, order.SkuID, order.UserID)

	// 恢复优惠券
	if order.CouponID > 0 {
		if err := q.mysql.RestoreCoupon(ctx, order.CouponID); err != nil {
			q.log.Warnf("恢复优惠券失败: %v", err)
		}
	}

	q.log.Infof("订单超时取消: %s", orderNo)
}
