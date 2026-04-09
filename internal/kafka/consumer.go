// internal/kafka/consumer.go
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-kratos/kratos/v2/log"
	"seckill-service/internal/biz"
	"seckill-service/internal/cache"
	"seckill-service/internal/mq"
)

const (
	MaxRetryCount  = 3
	RetryBaseDelay = 10 * time.Second
	MaxRetryDelay  = 10 * time.Minute
)

type Consumer struct {
	consumerGroup sarama.ConsumerGroup
	mysql         biz.SeckillRepo
	cache         biz.CacheRepo
	idempotent    *cache.IdempotentChecker
	producer      *Producer
	log           *log.Helper
	config        *Config

	// 优雅关闭
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// ConsumerHandler 实现 sarama.ConsumerGroupHandler
type ConsumerHandler struct {
	consumer *Consumer
	ready    chan bool
}

func NewConsumer(cfg *Config, mysql biz.SeckillRepo, cache biz.CacheRepo,
	idempotent *cache.IdempotentChecker, producer *Producer, logger log.Logger) (*Consumer, error) {

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid kafka config: %w", err)
	}

	saramaCfg := cfg.NewSaramaConfig()
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest

	consumerGroup, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroup, saramaCfg)
	if err != nil {
		return nil, fmt.Errorf("create consumer group failed: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Consumer{
		consumerGroup: consumerGroup,
		mysql:         mysql,
		cache:         cache,
		idempotent:    idempotent,
		producer:      producer,
		log:           log.NewHelper(log.With(logger, "module", "kafka/consumer")),
		config:        cfg,
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

// Start 启动消费者
func (c *Consumer) Start(ctx context.Context) error {
	handler := &ConsumerHandler{
		consumer: c,
		ready:    make(chan bool),
	}

	topics := []string{c.config.Topics.SeckillOrder}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			if err := c.consumerGroup.Consume(c.ctx, topics, handler); err != nil {
				c.log.Errorf("consume failed: %v", err)
			}
			if c.ctx.Err() != nil {
				return
			}
		}
	}()

	<-handler.ready
	c.log.Infof("consumer started, group=%s, topics=%v", c.config.ConsumerGroup, topics)
	return nil
}

// Stop 停止消费者
func (c *Consumer) Stop() error {
	c.log.Info("stopping consumer...")
	c.cancel()
	c.wg.Wait()

	if err := c.consumerGroup.Close(); err != nil {
		c.log.Errorf("close consumer group failed: %v", err)
		return err
	}

	c.log.Info("consumer stopped")
	return nil
}

// Setup 实现 sarama.ConsumerGroupHandler
func (h *ConsumerHandler) Setup(session sarama.ConsumerGroupSession) error {
	close(h.ready)
	return nil
}

// Cleanup 实现 sarama.ConsumerGroupHandler
func (h *ConsumerHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim 实现 sarama.ConsumerGroupHandler
func (h *ConsumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		h.consumer.processMessage(session.Context(), msg, session)
	}
	return nil
}

// processMessage 处理单条消息
func (c *Consumer) processMessage(ctx context.Context, msg *sarama.ConsumerMessage, session sarama.ConsumerGroupSession) {
	startTime := time.Now()

	// 获取重试次数
	retryCount := c.getRetryCount(msg)

	// 处理消息
	err := c.handle(ctx, msg)

	// 记录处理耗时
	elapsed := time.Since(startTime)
	if elapsed > 5*time.Second {
		c.log.WithContext(ctx).Warnf("message processing slow: %v, orderNo=%s", elapsed, c.getOrderNo(msg))
	}

	if err == nil {
		// 处理成功，提交 offset
		session.MarkMessage(msg, "")
		c.log.WithContext(ctx).Infof("message processed: partition=%d, offset=%d, retryCount=%d, elapsed=%v",
			msg.Partition, msg.Offset, retryCount, elapsed)
		return
	}

	// 处理失败
	c.handleFailure(ctx, msg, session, err, retryCount)
}

// handleFailure 处理失败消息
func (c *Consumer) handleFailure(ctx context.Context, msg *sarama.ConsumerMessage,
	session sarama.ConsumerGroupSession, err error, retryCount int) {

	// 超过最大重试次数
	if retryCount >= MaxRetryCount {
		c.log.WithContext(ctx).Warnf("max retry reached: orderNo=%s, retryCount=%d, err=%v",
			c.getOrderNo(msg), retryCount, err)

		// 发送到死信队列
		if orderMsg := c.parseOrderMessage(msg); orderMsg != nil {
			if dlqErr := c.producer.SendToDLQ(ctx, orderMsg, err.Error(), retryCount); dlqErr != nil {
				c.log.WithContext(ctx).Errorf("send to DLQ failed: %v", dlqErr)
			}
		}

		// 提交 offset，避免无限重试
		session.MarkMessage(msg, "")
		return
	}

	// 计算退避时间
	backoff := c.calculateBackoff(retryCount + 1)

	// 发送到重试队列
	if orderMsg := c.parseOrderMessage(msg); orderMsg != nil {
		if retryErr := c.producer.SendToRetry(ctx, orderMsg, retryCount+1, backoff); retryErr != nil {
			c.log.WithContext(ctx).Errorf("send to retry queue failed: %v", retryErr)
			// 如果发送到重试队列失败，直接提交 offset，避免消息堆积
			session.MarkMessage(msg, "")
			return
		}
	}

	// 提交当前 offset
	session.MarkMessage(msg, "")

	c.log.WithContext(ctx).Infof("message sent to retry: orderNo=%s, retryCount=%d, backoff=%v, err=%v",
		c.getOrderNo(msg), retryCount, backoff, err)
}

// handle 核心业务处理逻辑
func (c *Consumer) handle(ctx context.Context, msg *sarama.ConsumerMessage) error {
	orderMsg := c.parseOrderMessage(msg)
	if orderMsg == nil {
		return fmt.Errorf("parse message failed")
	}

	// 幂等检验
	isFirst, err := c.idempotent.CheckAndMark(ctx, orderMsg.OrderNo, 10*time.Minute)
	if err != nil {
		c.log.WithContext(ctx).Errorf("idempotent check failed: %v", err)
		return err
	}
	if !isFirst {
		c.log.WithContext(ctx).Infof("duplicate message skipped: orderNo=%s", orderMsg.OrderNo)
		return nil
	}

	// 获取地址信息
	address, err := c.mysql.GetUserAddress(ctx, orderMsg.AddressID)
	if err != nil {
		c.log.WithContext(ctx).Errorf("get address failed: %v", err)
		c.cache.RollbackStock(ctx, orderMsg.SkuID, orderMsg.Quantity)
		return err
	}

	// 构建订单对象
	order := &biz.Order{
		OrderNo:      orderMsg.OrderNo,
		UserID:       orderMsg.UserID,
		ActivityID:   orderMsg.ActivityID,
		ProductID:    orderMsg.ProductID,
		SkuID:        orderMsg.SkuID,
		SeckillPrice: orderMsg.SeckillPrice,
		Quantity:     int64(orderMsg.Quantity),
		AddressID:    orderMsg.AddressID,
		CouponID:     orderMsg.CouponID,
		Status:       biz.OrderStatusPending,
	}

	// 创建订单（事务中）
	orderNo, err := c.mysql.CreateOrder(ctx, order)
	if err != nil {
		c.log.WithContext(ctx).Errorf("create order failed: %v", err)
		c.cache.RollbackStock(ctx, orderMsg.SkuID, orderMsg.Quantity)
		return err
	}

	// 创建收货快照
	if err := c.mysql.CreateOrderShipping(ctx, orderNo, address); err != nil {
		c.log.WithContext(ctx).Errorf("create shipping failed: %v", err)
		// 不返回错误，只记录日志
	}

	// 扣减 MySQL 库存
	if err := c.mysql.DecreaseStock(ctx, orderMsg.SkuID, uint32(orderMsg.Quantity), orderMsg.Version); err != nil {
		c.log.WithContext(ctx).Errorf("decrease stock failed: %v", err)
		c.cache.RollbackStock(ctx, orderMsg.SkuID, orderMsg.Quantity)
		return err
	}

	c.log.WithContext(ctx).Infof("order created: orderNo=%s, userID=%d, skuID=%d, quantity=%d",
		orderNo, orderMsg.UserID, orderMsg.SkuID, orderMsg.Quantity)

	return nil
}

// 辅助方法
func (c *Consumer) parseOrderMessage(msg *sarama.ConsumerMessage) *mq.SeckillOrderMessage {
	var orderMsg mq.SeckillOrderMessage
	if err := json.Unmarshal(msg.Value, &orderMsg); err != nil {
		c.log.Errorf("unmarshal message failed: %v", err)
		return nil
	}
	return &orderMsg
}

func (c *Consumer) getRetryCount(msg *sarama.ConsumerMessage) int {
	for _, header := range msg.Headers {
		if string(header.Key) == "retry_count" {
			var count int
			fmt.Sscanf(string(header.Value), "%d", &count)
			return count
		}
	}
	return 0
}

func (c *Consumer) getOrderNo(msg *sarama.ConsumerMessage) string {
	for _, header := range msg.Headers {
		if string(header.Key) == "order_no" {
			return string(header.Value)
		}
	}
	return ""
}

func (c *Consumer) calculateBackoff(retryCount int) time.Duration {
	if retryCount <= 0 {
		return RetryBaseDelay
	}

	delay := RetryBaseDelay
	for i := 1; i < retryCount; i++ {
		delay *= 2
		if delay > MaxRetryDelay {
			return MaxRetryDelay
		}
	}
	return delay
}
