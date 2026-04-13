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

const (
	delayQueueKey    = "delay:orders"
	batchSize        = 100
	minPollInterval  = 1 * time.Second
	maxPollInterval  = 30 * time.Second
	maxCancelWorkers = 20
)

// DelayQueue 延迟队列：动态轮询+微取消协程
type DelayQueue struct {
	rdb       *redis.Client
	mysql     biz.SeckillRepo
	cache     biz.CacheRepo
	log       *log.Helper
	workerSem chan struct{}
}

func NewDelayQueue(rdb *redis.Client, mysql biz.SeckillRepo, cache biz.CacheRepo, logger log.Logger) *DelayQueue {
	return &DelayQueue{
		rdb:       rdb,
		mysql:     mysql,
		cache:     cache,
		log:       log.NewHelper(log.With(logger, "module", "job/delay")),
		workerSem: make(chan struct{}, maxCancelWorkers),
	}
}

// Add 添加延迟任务
func (q *DelayQueue) Add(ctx context.Context, orderNo string, delay time.Duration) error {
	score := float64(time.Now().Add(delay).Unix()) // 将时间转换为时间戳
	return q.rdb.ZAdd(ctx, delayQueueKey, &redis.Z{Score: score, Member: orderNo}).Err()
}

// Start 启动扫描
func (q *DelayQueue) Start(ctx context.Context) {
	q.log.Info("延迟队列启动")
	nextPoll := time.Now()

	for {
		waitDur := time.Until(nextPoll) // 计算需要等待的时长
		if waitDur < 0 {
			waitDur = 0
		}
		select {
		case <-ctx.Done():
			q.log.Info("延迟队列停止")
			return
		case <-time.After(waitDur):
			nextPoll = q.runOnce(ctx)
		}
	}
}

// runOnce 单次扫描
func (q *DelayQueue) runOnce(ctx context.Context) time.Time {
	now := float64(time.Now().Unix())
	orders, err := q.rdb.ZRangeByScoreWithScores(ctx, delayQueueKey, &redis.ZRangeBy{
		Min:    "-inf",                 // 最小
		Max:    fmt.Sprintf("%f", now), // 当前时间
		Offset: 0,
		Count:  int64(batchSize), // 每次最多100个
	}).Result()

	if err != nil {
		q.log.Errorf("扫描延迟队列失败: %v", err)
		return time.Now().Add(maxPollInterval)
	}

	// 处理到期的订单
	if len(orders) > 0 {
		for _, z := range orders {
			q.dispatchCancel(ctx, z.Member.(string))
		}

		// 批次满载，立即继续下一轮
		if len(orders) == batchSize {
			q.log.Infof("批次满载(%d)，立即继续下一轮", batchSize)
			return time.Now() // 立即返回，不等待
		}
	}

	// 根据最早任务计算下次轮询时间
	return q.nextPollTime(ctx)
}

// nextPollTime 计算下次轮询时间
func (q *DelayQueue) nextPollTime(ctx context.Context) time.Time {
	// 获取最早订单
	results, err := q.rdb.ZRangeWithScores(ctx, delayQueueKey, 0, 0).Result()

	if err != nil || len(results) == 0 {
		// 没有待处理订单，30秒后重试
		return time.Now().Add(maxPollInterval)
	}

	// 计算距离过期的时间
	expireAt := time.Unix(int64(results[0].Score), 0)
	waitDur := time.Until(expireAt)

	// 限制轮询间隔在 [1秒, 30秒] 范围内
	if waitDur < minPollInterval {
		waitDur = minPollInterval
	}
	if waitDur > maxPollInterval {
		waitDur = maxPollInterval
	}

	return time.Now().Add(waitDur)
}

// dispatchCancel 原子抢占
func (q *DelayQueue) dispatchCancel(ctx context.Context, orderNo string) {
	// 第一层防护：原子抢占（只一个节点处理订单）
	removed, err := q.rdb.ZRem(ctx, delayQueueKey, orderNo).Result()
	if err != nil {
		q.log.Errorf("ZRem 失败 orderNo=%s err=%v", orderNo, err)
		return
	}
	if removed == 0 {
		// 被其他节点抢占，直接返回
		return
	}

	// 第二层防护：并发控制
	select {
	case q.workerSem <- struct{}{}: // 占用一个协程，控制处理的超时订单数
	case <-ctx.Done():
		return
	}

	// 异步处理，避免阻塞扫描
	go func() {
		defer func() { <-q.workerSem }() // 释放信号量
		q.handleTimeout(ctx, orderNo)
	}()
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
	lockKey := fmt.Sprintf("lock:order:%s", orderNo) //分布式锁
	// 这里设置uuid是为了全局协调（自增ID、时间戳）和防止分布式环境重复（进程ID+线程ID）
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
		q.rdb.Eval(ctx, script, []string{lockKey}, lockValue)
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
		q.log.Errorf("取消订单失败 orderNo=%s err=%v", orderNo, err)
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

	q.log.Infof("订单超时取消成功: orderNo=%s,userID=%d,skuID=%d", orderNo, order.UserID, order.SkuID)
}
