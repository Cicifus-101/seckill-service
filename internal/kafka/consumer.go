package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"seckill-service/internal/biz"
	"seckill-service/internal/cache"
	"seckill-service/internal/mq"
	"time"
)

// Consumer MQ 消费者
type Consumer struct {
	rdb        *redis.Client
	mysql      biz.SeckillRepo
	cache      biz.CacheRepo
	idempotent *cache.IdempotentChecker
	log        *log.Helper
}

func NewConsumer(rdb *redis.Client, mysql biz.SeckillRepo, cache biz.CacheRepo, idempotent *cache.IdempotentChecker, logger log.Logger) *Consumer {
	return &Consumer{
		rdb:        rdb,
		mysql:      mysql,
		cache:      cache,
		idempotent: idempotent,
		log:        log.NewHelper(log.With(logger, "module", "mq/consumer")),
	}
}

// Start 启动消费者
func (c *Consumer) Start(ctx context.Context) {
	c.rdb.XGroupCreateMkStream(ctx, "seckill:orders", "order-group", "0")

	for {
		select {
		case <-ctx.Done():
			c.log.Info("消费者停止")
			return
		default:
			c.consume(ctx)
		}
	}
}

func (c *Consumer) consume(ctx context.Context) {
	streams, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    "order-group",
		Consumer: "consumer-1",
		Streams:  []string{"seckill:orders", ">"},
		Count:    10,
		Block:    1 * time.Second,
	}).Result()

	if err != nil || len(streams) == 0 {
		return
	}

	for _, stream := range streams {
		for _, msg := range stream.Messages {
			if err := c.handle(ctx, msg); err != nil {
				c.log.Errorf("处理消息失败：%v", err)
				continue
			}
			c.rdb.XAck(ctx, "seckill:orders", "order-group", msg.ID)
		}
	}
}

func (c *Consumer) handle(ctx context.Context, msg redis.XMessage) error {
	var orderMsg mq.SeckillOrderMessage
	data, ok := msg.Values["data"].(string)
	if !ok {
		return fmt.Errorf("invalid message format")
	}

	if err := json.Unmarshal([]byte(data), &orderMsg); err != nil {
		return err
	}

	// 幂等检验
	isFirst, err := c.idempotent.CheckAndMark(ctx, orderMsg.OrderNo, 10*time.Minute)
	if err != nil {
		c.log.Errorf("幂等检查失败：%v", err)
		return err
	}
	if !isFirst {
		c.log.Infof("重复消息，已跳过: orderNo=%s", orderMsg.OrderNo)
		return nil
	}

	// 获取地址
	address, err := c.mysql.GetUserAddress(ctx, orderMsg.AddressID)
	if err != nil {
		c.cache.RollbackStock(ctx, orderMsg.SkuID, orderMsg.Quantity)
		return err
	}

	order := &biz.Order{
		UserID:       orderMsg.UserID,
		ActivityID:   orderMsg.ActivityID,
		ProductID:    orderMsg.ProductID,
		SkuID:        orderMsg.SkuID,
		SeckillPrice: orderMsg.SeckillPrice,
		Quantity:     int64(orderMsg.Quantity),
		AddressID:    orderMsg.AddressID,
		CouponID:     orderMsg.CouponID,
	}

	orderNo, err := c.mysql.CreateOrder(ctx, order)
	if err != nil {
		c.cache.RollbackStock(ctx, orderMsg.SkuID, orderMsg.Quantity)
		return err
	}

	// 创建收获快照
	if err := c.mysql.CreateOrderShipping(ctx, orderNo, address); err != nil {
		return err
	}

	// 扣减 MySQL 库存
	if err := c.mysql.DecreaseStock(ctx, orderMsg.SkuID, uint32(orderMsg.Quantity), orderMsg.Version); err != nil {
		c.cache.RollbackStock(ctx, orderMsg.SkuID, orderMsg.Quantity)
		return err
	}

	c.log.Infof("订单创建成功: %s, user=%d", orderNo, orderMsg.UserID)
	return nil
}
