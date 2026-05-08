package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"seckill-service/internal/observability"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-kratos/kratos/v2/log"
	"seckill-service/internal/biz"
	"seckill-service/internal/mq"
)

const (
	MaxRetryCount  = 3
	RetryBaseDelay = 10 * time.Second
	MaxRetryDelay  = 10 * time.Minute
)

type Consumer struct {
	consumerGroup sarama.ConsumerGroup
	usecase       *biz.SeckillUsecase // 只依赖业务层
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
	consumer  *Consumer
	ready     chan bool
	readyOnce sync.Once //ConsumerGroup 重平衡时 Setup() 可能多次调用
}

func NewConsumer(cfg *Config, usecase *biz.SeckillUsecase, producer *Producer, logger log.Logger) (*Consumer, error) {

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid kafka config: %w", err)
	}

	saramaCfg := cfg.NewSaramaConfig()
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	saramaCfg.Consumer.Offsets.AutoCommit.Enable = true
	saramaCfg.Consumer.Return.Errors = true

	consumerGroup, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroup, saramaCfg)
	if err != nil {
		return nil, fmt.Errorf("create consumer group failed: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Consumer{
		consumerGroup: consumerGroup,
		usecase:       usecase,
		producer:      producer,
		log:           log.NewHelper(log.With(logger, "module", "kafka/consumer")),
		config:        cfg,
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

// Start 启动消费者
func (c *Consumer) Start(ctx context.Context) error {
	// 这个实现了消息处理器handler的接口
	handler := &ConsumerHandler{
		consumer: c,
		ready:    make(chan bool),
	}

	topics := []string{
		c.config.Topics.SeckillOrder,
		c.config.Topics.SeckillRetry,
	}

	go func() {
		for err := range c.consumerGroup.Errors() {
			if err != nil {
				c.log.Errorf("consumer group error: %v", err)
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			if err := c.consumerGroup.Consume(c.ctx, topics, handler); err != nil {
				c.log.Errorf("consume failed: %v", err)
				time.Sleep(2 * time.Second)
			}
			if c.ctx.Err() != nil {
				return
			}
		}
	}()

	select {
	case <-handler.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
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
	h.readyOnce.Do(func() {
		close(h.ready)
	})
	return nil
}

// Cleanup 实现 sarama.ConsumerGroupHandler
func (h *ConsumerHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim 实现 sarama.ConsumerGroupHandler 消息拉取
func (h *ConsumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	// 从一个分区中不断读取消息，session是消费
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
		session.Commit()
		c.log.WithContext(ctx).Infof("message processed: partition=%d, offset=%d, retryCount=%d, elapsed=%v",
			msg.Partition, msg.Offset, retryCount, elapsed)
		return
	}

	// shutdown/cancel是不要继续路由，避免误投 retry/DLQ
	if errors.Is(err, context.Canceled) {
		c.log.WithContext(ctx).Infof("message processing canceled: topic=%s partition=%d offset=%d",
			msg.Topic, msg.Partition, msg.Offset)
		return
	}

	// 处理失败
	commit, routeErr := c.routeFailure(ctx, msg, err)
	if routeErr != nil {
		c.log.WithContext(ctx).Errorf("route failure failed: topic=%s partition=%d offset=%d err=%v", msg.Topic, msg.Partition, msg.Offset, routeErr)
	}
	if commit {
		session.MarkMessage(msg, "")
		session.Commit()
	}
}

// handle 核心业务处理逻辑
func (c *Consumer) handle(ctx context.Context, msg *sarama.ConsumerMessage) (err error) {
	start := time.Now()
	ctx = observability.ExtractKafkaContext(ctx, msg.Headers)
	ctx, span := observability.Start(ctx, "kafka.consume."+msg.Topic)
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("kafka", "consume_"+msg.Topic, result, start)
	}()

	switch msg.Topic {
	case c.config.Topics.SeckillOrder:
		return c.handleOrderTopic(ctx, msg)
	case c.config.Topics.SeckillRetry:
		return c.handleRetryTopic(ctx, msg)
	default:
		return fmt.Errorf("unknown topic: %s", msg.Topic)
	}
}

// handleOrderTopic 首次消费
func (c *Consumer) handleOrderTopic(ctx context.Context, msg *sarama.ConsumerMessage) error {
	orderMsg, err := c.parseOrderMessage(msg)
	if err != nil {
		return err
	}

	err = c.usecase.ConfirmSeckillOrder(ctx, orderMsg)
	if err == nil || errors.Is(err, biz.ErrOrderExists) {
		if _, markErr := c.usecase.Idempotent.CheckAndMark(ctx, orderMsg.OrderNo, 24*time.Hour); markErr != nil {
			c.log.WithContext(ctx).Warnf("mark order consumed failed: orderNo=%s, err=%v", orderMsg.OrderNo, markErr)
		}
		return nil
	}

	return err
}

// handleRetryTopic 执行业务重试
func (c *Consumer) handleRetryTopic(ctx context.Context, msg *sarama.ConsumerMessage) error {
	orderMsg, err := c.parseOrderMessage(msg)
	if err != nil {
		return err
	}

	// 读取延迟时间
	if wait := c.getRetryWait(msg); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 再执行一次业务
	err = c.usecase.ConfirmSeckillOrder(ctx, orderMsg)
	if err == nil || errors.Is(err, biz.ErrOrderExists) {
		return nil
	}

	// 重试失败继续分流
	return err
}

// routeFailure 处理失败消息并决定是否提交offset
func (c *Consumer) routeFailure(ctx context.Context, msg *sarama.ConsumerMessage, err error) (bool, error) {
	orderMsg, parseErr := c.parseOrderMessage(msg)
	if parseErr != nil {
		// 解析失败，直接进 DLQ
		if dlqErr := c.producer.SendParseFailureToDLQ(ctx, msg.Value, msg.Topic, msg.Partition, msg.Offset, parseErr.Error(), msg.Headers); dlqErr != nil {
			return false, dlqErr
		}
		c.log.WithContext(ctx).Warnf("parse failure routed to DLQ: topic=%s partition=%d offset=%d err=%v",
			msg.Topic, msg.Partition, msg.Offset, parseErr,
		)
		return true, nil
	}

	retryCount := c.getRetryCount(msg)
	switch classifyOrderError(err) {
	case failureRetry:
		if retryCount >= MaxRetryCount {
			return c.handleFinalFailture(ctx, msg, orderMsg, err)
		}

		backoff := c.calculateBackoff(retryCount + 1)
		if retryErr := c.producer.SendToRetry(ctx, orderMsg, retryCount+1, backoff); retryErr != nil {
			return false, retryErr
		}
		c.log.WithContext(ctx).Infof("message sent to retry: orderNo=%s retryCount=%d backoff=%v err=%v", orderMsg.OrderNo, retryCount+1, backoff, err)
		return true, nil

	case failureDLQ:
		return c.handleFinalFailture(ctx, msg, orderMsg, err)

	case failureDrop: // 直接丢弃
		return true, nil
	}

	return false, nil
}

func (c *Consumer) handleFinalFailture(ctx context.Context, msg *sarama.ConsumerMessage, orderMsg *mq.SeckillOrderMessage, err error) (bool, error) {
	// 先写 DLQ
	if dlqErr := c.producer.SendToDLQ(ctx, orderMsg, err.Error(), c.getRetryCount(msg), msg.Topic, msg.Partition, msg.Offset); dlqErr != nil {
		return false, dlqErr
	}

	// 业务补偿
	if rbErr := c.usecase.RollbackSeckillReservation(ctx, orderMsg); rbErr != nil {
		c.log.WithContext(ctx).Errorf(
			"rollback seckill reservation failed: orderNo=%s activityID=%d skuID=%d err=%v", orderMsg.OrderNo, orderMsg.ActivityID, orderMsg.SkuID, rbErr,
		)
		return false, rbErr
	}

	c.log.WithContext(ctx).Warnf(
		"final failure handled: orderNo=%s retryCount=%d err=%v", orderMsg.OrderNo, c.getRetryCount(msg), err)
	return true, nil
}

// 辅助方法
func (c *Consumer) parseOrderMessage(msg *sarama.ConsumerMessage) (*mq.SeckillOrderMessage, error) {
	var orderMsg mq.SeckillOrderMessage
	if err := json.Unmarshal(msg.Value, &orderMsg); err != nil {
		return nil, err
	}
	return &orderMsg, nil
}

func (c *Consumer) getRetryCount(msg *sarama.ConsumerMessage) int {
	if v, ok := c.getHeaderValue(msg, "retry_count"); ok {
		count, err := strconv.Atoi(v)
		if err == nil && count >= 0 {
			return count
		}
	}
	return 0
}

func (c *Consumer) getOrderNo(msg *sarama.ConsumerMessage) string {
	if v, ok := c.getHeaderValue(msg, "order_no"); ok {
		return v
	}
	return ""
}

func (c *Consumer) getRetryWait(msg *sarama.ConsumerMessage) time.Duration {
	v, ok := c.getHeaderValue(msg, "next_retry_at")
	if !ok || v == "" {
		return 0
	}

	ts, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ts <= 0 {
		return 0
	}

	now := time.Now().Unix()
	if ts <= now {
		return 0
	}

	return time.Until(time.Unix(ts, 0))
}

func (c *Consumer) getHeaderValue(msg *sarama.ConsumerMessage, key string) (string, bool) {
	for _, header := range msg.Headers {
		if strings.EqualFold(string(header.Key), key) {
			return string(header.Value), true
		}
	}
	return "", false
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
