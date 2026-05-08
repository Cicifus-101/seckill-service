// internal/kafka/producer.go
package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"seckill-service/internal/observability"
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
func (p *Producer) SendEvent(ctx context.Context, topic string, key string, payload []byte, headers map[string]string) error {
	producerMsg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(payload),
	}

	traceHeaders := observability.InjectKafkaHeaders(ctx)
	for k, v := range traceHeaders {
		producerMsg.Headers = append(producerMsg.Headers, sarama.RecordHeader{
			Key:   []byte(k),
			Value: []byte(v),
		})
	}

	// 添加业务自定义消息头
	for k, v := range headers {
		producerMsg.Headers = append(producerMsg.Headers, sarama.RecordHeader{
			Key:   []byte(k),
			Value: []byte(v),
		})
	}

	_, _, err := p.syncProducer.SendMessage(producerMsg)
	return err
}

func (p *Producer) Send(ctx context.Context, msg *mq.SeckillOrderMessage) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "kafka.produce.SeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("kafka", "produce_seckill_order", result, start)
	}()

	msg.TraceID = observability.TraceID(ctx)

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"retry_count": "0",
		"order_no":    msg.OrderNo,
		"trace_id":    msg.TraceID,
		"request_id":  msg.RequestID,
	}

	return p.SendEvent(ctx, p.orderTopic, msg.OrderNo, data, headers)
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
func (p *Producer) SendToDLQ(ctx context.Context, msg *mq.SeckillOrderMessage, reason string, retryCount int, topic string, partition int32, offset int64) error {
	msg.TraceID = observability.TraceID(ctx)

	dlqMsg := &mq.DeadLetterMessage{
		EventID:     msg.EventID,
		Topic:       topic,
		Partition:   partition,
		Offset:      offset,
		OriginalMsg: *msg,
		RawPayload:  "",
		LastError:   reason,
		RetryCount:  retryCount,
		NextRetryAt: time.Now().Unix(),
	}

	payload, err := json.Marshal(dlqMsg)
	if err != nil {
		return fmt.Errorf("marshal dlq message failed: %w", err)
	}

	headers := map[string]string{
		"retry_count": fmt.Sprintf("%d", retryCount),
		"reason":      reason,
		"trace_id":    msg.TraceID,
		"order_no":    msg.OrderNo,
		"event_id":    msg.EventID,
	}

	return p.SendEvent(ctx, p.dlqTopic, msg.OrderNo, payload, headers)
}

// SendToRetry 发送到重试队列
func (p *Producer) SendToRetry(ctx context.Context, msg *mq.SeckillOrderMessage, retryCount int, delay time.Duration) error {
	msg.RetryCount = retryCount
	msg.TraceID = observability.TraceID(ctx)
	msg.Timestamp = time.Now().Unix()

	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	headers := map[string]string{
		"retry_count":    fmt.Sprintf("%d", retryCount),
		"next_retry_at":  fmt.Sprintf("%d", time.Now().Add(delay).Unix()),
		"trace_id":       msg.TraceID,
		"order_no":       msg.OrderNo,
		"event_id":       msg.EventID,
		"request_id":     msg.RequestID,
		"original_topic": p.orderTopic,
	}

	return p.SendEvent(ctx, p.retryTopic, msg.OrderNo, payload, headers)
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

// SendParseFailureToDLQ 消息解析失败
func (p *Producer) SendParseFailureToDLQ(ctx context.Context, raw []byte, topic string, partition int32, offset int64, reason string, headers []*sarama.RecordHeader) error {
	meta := map[string]string{}
	for _, h := range headers {
		meta[string(h.Key)] = string(h.Value)
	}

	dlqMsg := &mq.DeadLetterMessage{
		EventID:     meta["event_id"],
		Topic:       topic,
		Partition:   partition,
		Offset:      offset,
		RawPayload:  string(raw), // 原始消息的字节数据
		LastError:   reason,
		RetryCount:  0,
		NextRetryAt: time.Now().Unix(),
	}

	payload, err := json.Marshal(dlqMsg)
	if err != nil {
		return err
	}

	return p.SendEvent(ctx, p.dlqTopic, "parse-error", payload, map[string]string{
		"reason":   reason,
		"topic":    topic,
		"trace_id": meta["trace_id"],
	})
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
