package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/IBM/sarama"
	"github.com/go-kratos/kratos/v2/log"
	"seckill-service/internal/biz"
	"seckill-service/internal/mq"
	"seckill-service/internal/observability"
	"sync"
	"time"
)

type DLQConsumer struct {
	consumerGroup sarama.ConsumerGroup
	repo          biz.SeckillRepo
	log           *log.Helper
	config        *Config

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

type DLQConsumerHandler struct {
	consumer  *DLQConsumer
	ready     chan bool
	readyOnce sync.Once
}

func NewDLQConsumer(cfg *Config, repo biz.SeckillRepo, logger log.Logger) (*DLQConsumer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid kafka config: %w", err)
	}

	saramaCfg := cfg.NewSaramaConfig()
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	saramaCfg.Consumer.Offsets.AutoCommit.Enable = true
	saramaCfg.Consumer.Return.Errors = true

	consumerGroup, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroup+"-dlq", saramaCfg)
	if err != nil {
		return nil, fmt.Errorf("create dlq consumer group failed: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	return &DLQConsumer{
		consumerGroup: consumerGroup,
		repo:          repo,
		log:           log.NewHelper(log.With(logger, "module", "kafka/dlq-consumer")),
		config:        cfg,
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

func (c *DLQConsumer) Start(ctx context.Context) error {
	handler := &DLQConsumerHandler{
		consumer: c,
		ready:    make(chan bool),
	}

	topics := []string{c.config.Topics.SeckillDLQ}

	go func() {
		for err := range c.consumerGroup.Errors() {
			if err != nil {
				c.log.Errorf("dlq consumer group error: %v", err)
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			if err := c.consumerGroup.Consume(c.ctx, topics, handler); err != nil {
				c.log.Errorf("dlq consume failed: %v", err)
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

	c.log.Infof("dlq consumer started, group=%s, topic=%s", c.config.ConsumerGroup+"-dlq", c.config.Topics.SeckillDLQ)
	return nil
}

func (c *DLQConsumer) Stop() error {
	c.cancel()
	c.wg.Wait()
	return c.consumerGroup.Close()
}

func (h *DLQConsumerHandler) Setup(session sarama.ConsumerGroupSession) error {
	h.readyOnce.Do(func() { close(h.ready) })
	return nil
}

func (h *DLQConsumerHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	return nil
}

func (h *DLQConsumerHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		h.consumer.processMessage(session.Context(), msg, session)
	}
	return nil
}

func (c *DLQConsumer) processMessage(ctx context.Context, msg *sarama.ConsumerMessage, session sarama.ConsumerGroupSession) {
	err := c.handle(ctx, msg)
	if err != nil {
		c.log.WithContext(ctx).Errorf("dlq message handle failed: topic=%s partition=%d offset=%d err=%v",
			msg.Topic, msg.Partition, msg.Offset, err)
		return
	}
	session.MarkMessage(msg, "")
	session.Commit()
}

func (c *DLQConsumer) handle(ctx context.Context, msg *sarama.ConsumerMessage) (err error) {
	start := time.Now()
	ctx = observability.ExtractKafkaContext(ctx, msg.Headers)
	ctx, span := observability.Start(ctx, "kafka.dlq.consume")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("kafka", "consume_dlq", result, start)
	}()

	var dlqMsg mq.DeadLetterMessage
	if err := json.Unmarshal(msg.Value, &dlqMsg); err != nil {
		return err
	}

	if dlqMsg.EventID == "" {
		dlqMsg.EventID = fmt.Sprintf("dlq:%s:%d:%d", msg.Topic, msg.Partition, msg.Offset)
	}

	// 解析失败时，rawPayload是原始业务消息；业务失败时，自动回到原始订单消息JSON
	rawPayload := dlqMsg.RawPayload
	if rawPayload == "" { // 不是解析失败，如果解析失败（即原本消息json就受损），此时DLQ里面存储原始kafka消息字符串
		if b, marshalErr := json.Marshal(dlqMsg.OriginalMsg); marshalErr == nil {
			rawPayload = string(b)
		}
	}

	record := &biz.DeadLetterMessage{
		EventID:      dlqMsg.EventID,
		Topic:        dlqMsg.Topic,
		Partition:    dlqMsg.Partition,
		Offset:       dlqMsg.Offset,
		OrderNo:      dlqMsg.OriginalMsg.OrderNo,
		RequestID:    dlqMsg.OriginalMsg.RequestID,
		TraceID:      dlqMsg.OriginalMsg.TraceID,
		RetryCount:   dlqMsg.RetryCount,
		ErrorMessage: dlqMsg.LastError,
		RawPayload:   rawPayload,
		Status:       biz.DeadLetterStatusPending,
	}

	if err := c.repo.CreateDeadLetterMessage(ctx, record); err != nil {
		return err
	}

	return nil
}
