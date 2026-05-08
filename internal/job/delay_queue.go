package job

import (
	"context"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"seckill-service/internal/biz"
	"seckill-service/internal/observability"
	"time"
)

const (
	delayQueueKey      = "delay:orders"
	delayProcessingKey = "delay:orders:processing"
	batchSize          = 100
	minPollInterval    = 1 * time.Second
	maxPollInterval    = 30 * time.Second
	maxCancelWorkers   = 20
	processingTimeout  = 60 * time.Second
)

// DelayQueue 延迟队列：动态轮询+微取消协程
type DelayQueue struct {
	rdb       *redis.Client
	mysql     biz.SeckillRepo
	cache     biz.CacheRepo
	log       *log.Helper
	canceler  *biz.OrderCancelService
	workerSem chan struct{}
}

func NewDelayQueue(rdb *redis.Client, mysql biz.SeckillRepo, cache biz.CacheRepo, canceler *biz.OrderCancelService, logger log.Logger) *DelayQueue {
	return &DelayQueue{
		rdb:       rdb,
		mysql:     mysql,
		cache:     cache,
		canceler:  canceler,
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
	start := time.Now()
	ctx, span := observability.Start(ctx, "job.DelayQueue.Start")
	defer func() {
		observability.Finish(span, nil)
		observability.ObserveOperation("job", "delay_queue_Start", "success", start)
	}()

	elector := NewLeaderElector(q.cache, "seckill:leader:delay_queue", 15*time.Second, q.log.Logger())
	_ = elector.Run(ctx, func(runCtx context.Context) error {
		q.log.Info("delay queue leader started")
		nextPoll := time.Now()

		for {
			waitDur := time.Until(nextPoll)
			if waitDur < 0 {
				waitDur = 0
			}

			select {
			case <-runCtx.Done():
				return nil
			case <-time.After(waitDur):
				nextPoll = q.runOnce(runCtx)
			}
		}
	})
}

// runOnce 单次扫描
func (q *DelayQueue) runOnce(ctx context.Context) time.Time {
	start := time.Now()
	ctx, span := observability.Start(ctx, "job.DelayQueue.runOnce")
	defer func() {
		observability.Finish(span, nil)
		observability.ObserveOperation("job", "delay_queue_runOnce", "success", start)
	}()

	q.requeueExpiredProcessing(ctx)

	orders, err := q.claimDueOrders(ctx, batchSize)

	if err != nil {
		q.log.Errorf("扫描延迟队列失败: %v", err)
		return time.Now().Add(maxPollInterval)
	}

	if len(orders) > 0 {
		for _, orderNo := range orders {
			q.dispatchCancel(ctx, orderNo)
		}

		if len(orders) == batchSize {
			return time.Now()
		}
	}
	// 根据最早任务计算下次轮询时间
	return q.nextPollTime(ctx)
}

// claimDueOrders 原子移动到队列
func (q *DelayQueue) claimDueOrders(ctx context.Context, limit int) ([]string, error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "job.DelayQueue.claimDueOrders")
	defer func() {
		observability.Finish(span, nil)
		observability.ObserveOperation("job", "delay_queue_claimDueOrders", "success", start)
	}()

	now := time.Now().Unix()
	processingExpireAt := time.Now().Add(processingTimeout).Unix()

	script := `
local items = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[3])
local claimed = {}
for _, item in ipairs(items) do
    if redis.call("ZREM", KEYS[1], item) == 1 then
        redis.call("ZADD", KEYS[2], ARGV[2], item)
        table.insert(claimed, item)
    end
end
return claimed
`

	res, err := q.rdb.Eval(ctx, script, []string{delayQueueKey, delayProcessingKey}, now, processingExpireAt, limit).StringSlice()
	if err != nil {
		return nil, err
	}
	return res, nil
}

// requeueExpiredProcessing 将处理超时的任务移回主队列
func (q *DelayQueue) requeueExpiredProcessing(ctx context.Context) {
	now := time.Now().Unix()

	script := `
local items = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, item in ipairs(items) do
    if redis.call("ZREM", KEYS[1], item) == 1 then
        redis.call("ZADD", KEYS[2], ARGV[1], item)
    end
end
return items
`

	res, err := q.rdb.Eval(ctx, script, []string{delayProcessingKey, delayQueueKey}, now, batchSize).StringSlice()
	if err != nil {
		q.log.Warnf("恢复 processing 超时任务失败: %v", err)
		return
	}

	if len(res) > 0 {
		q.log.Warnf("恢复 processing 超时任务: count=%d", len(res))
	}
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
	start := time.Now()
	ctx, span := observability.Start(ctx, "job.DelayQueue.dispatchCancel")
	defer func() {
		observability.Finish(span, nil)
		observability.ObserveOperation("job", "delay_queue_dispatchCancel", "success", start)
	}()

	// 第二层防护：并发控制
	select {
	// 带缓冲的channel，在容量之内可以立即成功，当满了之后发送操作会阻塞等待
	case q.workerSem <- struct{}{}: // 占用一个协程，控制处理的超时订单数
	case <-ctx.Done():
		return
	}

	// 异步处理，避免阻塞扫描
	go func() {
		defer func() { <-q.workerSem }() // 释放信号量
		if err := q.handleTimeout(ctx, orderNo); err != nil {
			q.log.Warnf("处理超时订单失败: orderNo=%s err=%v", orderNo, err)
			return
		}

		if err := q.rdb.ZRem(ctx, delayProcessingKey, orderNo).Err(); err != nil {
			q.log.Warnf("删除 processing 任务失败: orderNo=%s err=%v", orderNo, err)
		}
	}()
}

// handleTimeout 执行真正的订单取消
func (q *DelayQueue) handleTimeout(ctx context.Context, orderNo string) error {
	start := time.Now()
	ctx, span := observability.Start(ctx, "job.DelayQueue.handleTimeout")
	defer func() {
		observability.Finish(span, nil)
		observability.ObserveOperation("job", "delay_queue_handleTimeout", "success", start)
	}()

	// 订单取消执行的唯一性（防止延迟队列、对账任务、手工补偿同时取消，同时改订单）
	// 防止同一个节点重复调度、goroutine重入
	// 或者节点A慢处理，定时补偿任务发现这个订单还是待支付，另一个地方又发起取消
	lockKey := fmt.Sprintf("lock:order:%s", orderNo)
	// 这里设置uuid是为了全局协调（自增ID、时间戳）和防止分布式环境重复（进程ID+线程ID）
	lockValue := uuid.New().String()
	lock, err := q.rdb.SetNX(ctx, lockKey, lockValue, 30*time.Second).Result()
	if err != nil {
		return err
	}
	if !lock {
		return fmt.Errorf("order cancel lock busy: %s", orderNo)
	}

	// 检验之后再删除，防止误删他人锁
	defer func() {
		script := `
            if redis.call("get", KEYS[1]) == ARGV[1] then
                return redis.call("del", KEYS[1])
            else
                return 0
            end`
		_ = q.rdb.Eval(ctx, script, []string{lockKey}, lockValue).Err()
	}()

	return q.canceler.CancelTimeoutOrder(ctx, orderNo, "订单超时未支付")
}
