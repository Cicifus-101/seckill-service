// internal/kafka/producer.go
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/go-kratos/kratos/v2/log"
	"seckill-service/internal/mq"
)

type Producer struct {
	syncProducer  sarama.SyncProducer
	asyncProducer sarama.AsyncProducer
	log           *log.Helper
	config        *Config

	// 主题
	orderTopic  string
	retryTopic  string
	dlqTopic    string
	resultTopic string

	// 关闭信号
	closeOnce sync.Once
	closeCh   chan struct{}
}

func NewProducer(cfg *Config, logger log.Logger) (*Producer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid kafka config: %w", err)
	}

	saramaCfg := cfg.NewSaramaConfig()

	// 创建同步生产者
	syncProducer, err := sarama.NewSyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		return nil, fmt.Errorf("create sync producer failed: %w", err)
	}

	// 创建异步生产者
	asyncProducer, err := sarama.NewAsyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		syncProducer.Close()
		return nil, fmt.Errorf("create async producer failed: %w", err)
	}

	p := &Producer{
		syncProducer:  syncProducer,
		asyncProducer: asyncProducer,
		log:           log.NewHelper(log.With(logger, "module", "kafka/producer")),
		config:        cfg,
		orderTopic:    cfg.Topics.SeckillOrder,
		retryTopic:    cfg.Topics.SeckillRetry,
		dlqTopic:      cfg.Topics.SeckillDLQ,
		resultTopic:   cfg.Topics.SeckillResult,
		closeCh:       make(chan struct{}),
	}

	// 启动错误处理协程
	go p.handleAsyncErrors()

	p.log.Infof("Kafka producer initialized, brokers=%v, topics=%v",
		cfg.Brokers, cfg.Topics)

	return p, nil
}

// handleAsyncErrors 处理异步发送错误
func (p *Producer) handleAsyncErrors() {
	for {
		select {
		case err := <-p.asyncProducer.Errors():
			if err != nil {
				p.log.Errorf("async send error: %v", err)
			}
		case <-p.closeCh:
			return
		}
	}
}

// Send 同步发送消息
func (p *Producer) Send(ctx context.Context, msg *mq.SeckillOrderMessage) error {
	msg.Timestamp = time.Now().Unix()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message failed: %w", err)
	}

	producerMsg := &sarama.ProducerMessage{
		Topic: p.orderTopic,
		Key:   sarama.StringEncoder(msg.OrderNo),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("retry_count"), Value: []byte("0")},
			{Key: []byte("order_no"), Value: []byte(msg.OrderNo)},
			{Key: []byte("user_id"), Value: []byte(fmt.Sprintf("%d", msg.UserID))},
			{Key: []byte("timestamp"), Value: []byte(fmt.Sprintf("%d", msg.Timestamp))},
		},
	}

	partition, offset, err := p.syncProducer.SendMessage(producerMsg)
	if err != nil {
		p.log.WithContext(ctx).Errorf("send message failed: %v", err)
		return p.SendToDLQ(ctx, msg, err.Error(), 0)
	}

	p.log.WithContext(ctx).Infof("message sent: orderNo=%s, topic=%s, partition=%d, offset=%d",
		msg.OrderNo, p.orderTopic, partition, offset)
	return nil
}

// SendAsync 异步发送
func (p *Producer) SendAsync(ctx context.Context, msg *mq.SeckillOrderMessage) {
	msg.Timestamp = time.Now().Unix()

	data, err := json.Marshal(msg)
	if err != nil {
		p.log.WithContext(ctx).Errorf("marshal message failed: %v", err)
		return
	}

	producerMsg := &sarama.ProducerMessage{
		Topic: p.orderTopic,
		Key:   sarama.StringEncoder(msg.OrderNo),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("retry_count"), Value: []byte("0")},
			{Key: []byte("order_no"), Value: []byte(msg.OrderNo)},
			{Key: []byte("user_id"), Value: []byte(fmt.Sprintf("%d", msg.UserID))},
			{Key: []byte("timestamp"), Value: []byte(fmt.Sprintf("%d", msg.Timestamp))},
		},
	}

	select {
	case p.asyncProducer.Input() <- producerMsg:
		p.log.WithContext(ctx).Debugf("async message queued: orderNo=%s", msg.OrderNo)
	default:
		p.log.WithContext(ctx).Warnf("async producer input channel full, orderNo=%s", msg.OrderNo)
	}
}

// SendToDLQ 发送到死信队列
func (p *Producer) SendToDLQ(ctx context.Context, msg *mq.SeckillOrderMessage, reason string, retryCount int) error {
	dlqMsg := &mq.DeadLetterMessage{
		OriginalMsg: *msg,
		RetryCount:  retryCount,
		LastError:   reason,
		NextRetryAt: time.Now().Unix(),
	}

	data, err := json.Marshal(dlqMsg)
	if err != nil {
		return fmt.Errorf("marshal dlq message failed: %w", err)
	}

	producerMsg := &sarama.ProducerMessage{
		Topic: p.dlqTopic,
		Key:   sarama.StringEncoder(msg.OrderNo),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("retry_count"), Value: []byte(fmt.Sprintf("%d", retryCount))},
			{Key: []byte("reason"), Value: []byte(reason)},
		},
	}

	_, _, err = p.syncProducer.SendMessage(producerMsg)
	if err != nil {
		p.log.WithContext(ctx).Errorf("send to DLQ failed: %v", err)
		return err
	}

	p.log.WithContext(ctx).Warnf("message sent to DLQ: orderNo=%s, reason=%s, retryCount=%d",
		msg.OrderNo, reason, retryCount)
	return nil
}

// SendToRetry 发送到重试队列
func (p *Producer) SendToRetry(ctx context.Context, msg *mq.SeckillOrderMessage, retryCount int, delay time.Duration) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	nextRetryAt := time.Now().Add(delay).Unix()

	producerMsg := &sarama.ProducerMessage{
		Topic: p.retryTopic,
		Key:   sarama.StringEncoder(msg.OrderNo),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("retry_count"), Value: []byte(fmt.Sprintf("%d", retryCount))},
			{Key: []byte("next_retry_at"), Value: []byte(fmt.Sprintf("%d", nextRetryAt))},
			{Key: []byte("delay"), Value: []byte(delay.String())},
		},
	}

	_, _, err = p.syncProducer.SendMessage(producerMsg)
	if err != nil {
		p.log.WithContext(ctx).Errorf("send to retry queue failed: %v", err)
		return err
	}

	p.log.WithContext(ctx).Infof("message sent to retry queue: orderNo=%s, retryCount=%d, delay=%v",
		msg.OrderNo, retryCount, delay)
	return nil
}

// SendResult 发送秒杀结果
func (p *Producer) SendResult(ctx context.Context, result *mq.SeckillResultMessage) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}

	producerMsg := &sarama.ProducerMessage{
		Topic: p.resultTopic,
		Key:   sarama.StringEncoder(result.OrderNo),
		Value: sarama.ByteEncoder(data),
		Headers: []sarama.RecordHeader{
			{Key: []byte("user_id"), Value: []byte(fmt.Sprintf("%d", result.UserID))},
			{Key: []byte("status"), Value: []byte(fmt.Sprintf("%d", result.Status))},
		},
	}

	_, _, err = p.syncProducer.SendMessage(producerMsg)
	return err
}

// Close 关闭生产者
func (p *Producer) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.closeCh)

		if p.syncProducer != nil {
			if cerr := p.syncProducer.Close(); cerr != nil {
				err = cerr
			}
		}
		if p.asyncProducer != nil {
			if cerr := p.asyncProducer.Close(); cerr != nil {
				err = cerr
			}
		}
	})
	return err
}
