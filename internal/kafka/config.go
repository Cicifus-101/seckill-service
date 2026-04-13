package kafka

import (
	"fmt"
	"github.com/IBM/sarama"
	"seckill-service/internal/conf"
	"time"
)

// TopicNames 主题名称配置
type TopicNames struct {
	SeckillOrder  string
	SeckillResult string
	SeckillDLQ    string
	SeckillRetry  string
}

type Config struct {
	Brokers       []string
	Version       string
	ConsumerGroup string
	ClientID      string

	// Producer 配置
	Producer ProducerConfig

	// Consumer 配置
	Consumer ConsumerConfig

	// Topic 配置
	Topics TopicNames
}

type ProducerConfig struct {
	MaxMessageBytes int
	RequiredAcks    int
	Compression     string
	RetryMax        int
	RetryBackoff    time.Duration
	Timeout         time.Duration
}

type ConsumerConfig struct {
	SessionTimeout    time.Duration
	HeartbeatInterval time.Duration
	MaxProcessingTime time.Duration
	FetchMinBytes     int
	FetchMaxBytes     int
	MaxWaitTime       time.Duration
}

// FromProto 从 proto 配置创建 Config（解耦配置框架和业务代码）
func FromProto(cfg *conf.Kafka) *Config {
	if cfg == nil {
		return nil
	}

	return &Config{
		Brokers:       cfg.Brokers,
		Version:       cfg.Version,
		ConsumerGroup: cfg.ConsumerGroup,
		ClientID:      cfg.ClientId,
		Producer: ProducerConfig{
			MaxMessageBytes: int(cfg.Producer.MaxMessageBytes),
			RequiredAcks:    int(cfg.Producer.RequiredAcks),
			Compression:     cfg.Producer.Compression,
			RetryMax:        int(cfg.Producer.RetryMax),
			RetryBackoff:    cfg.Producer.RetryBackoff.AsDuration(),
			Timeout:         cfg.Producer.Timeout.AsDuration(),
		},
		Consumer: ConsumerConfig{
			SessionTimeout:    cfg.Consumer.SessionTimeout.AsDuration(),
			HeartbeatInterval: cfg.Consumer.HeartbeatInterval.AsDuration(),
			MaxProcessingTime: cfg.Consumer.MaxProcessingTime.AsDuration(),
			FetchMinBytes:     int(cfg.Consumer.FetchMinBytes),
			FetchMaxBytes:     int(cfg.Consumer.FetchMaxBytes),
			MaxWaitTime:       cfg.Consumer.MaxWaitTime.AsDuration(),
		},
		Topics: TopicNames{
			SeckillOrder:  cfg.Topics.SeckillOrder,
			SeckillResult: cfg.Topics.SeckillResult,
			SeckillDLQ:    cfg.Topics.SeckillDlq,
			SeckillRetry:  cfg.Topics.SeckillRetry,
		},
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	if len(c.Brokers) == 0 {
		return fmt.Errorf("brokers is required")
	}
	if c.ConsumerGroup == "" {
		return fmt.Errorf("consumer_group is required")
	}
	if c.Topics.SeckillOrder == "" {
		return fmt.Errorf("seckill_order topic is required")
	}
	return nil
}

// NewSaramaConfig 创建 Sarama 配置
func (c *Config) NewSaramaConfig() *sarama.Config {
	version, err := sarama.ParseKafkaVersion(c.Version)
	if err != nil {
		version = sarama.V2_6_0_0
	}

	config := sarama.NewConfig()
	config.Version = version
	config.ClientID = c.ClientID

	// Producer 配置
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Producer.RequiredAcks = sarama.RequiredAcks(c.Producer.RequiredAcks)
	config.Producer.MaxMessageBytes = c.Producer.MaxMessageBytes
	config.Producer.Retry.Max = c.Producer.RetryMax
	config.Producer.Retry.Backoff = c.Producer.RetryBackoff
	config.Producer.Timeout = c.Producer.Timeout

	// 压缩配置
	switch c.Producer.Compression {
	case "gzip":
		config.Producer.Compression = sarama.CompressionGZIP
	case "snappy":
		config.Producer.Compression = sarama.CompressionSnappy
	case "lz4":
		config.Producer.Compression = sarama.CompressionLZ4
	case "zstd":
		config.Producer.Compression = sarama.CompressionZSTD
	default:
		config.Producer.Compression = sarama.CompressionNone
	}

	// Consumer 配置
	config.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	config.Consumer.Group.Session.Timeout = c.Consumer.SessionTimeout
	config.Consumer.Group.Heartbeat.Interval = c.Consumer.HeartbeatInterval
	config.Consumer.MaxProcessingTime = c.Consumer.MaxProcessingTime
	config.Consumer.Fetch.Min = int32(c.Consumer.FetchMinBytes)
	config.Consumer.Fetch.Default = int32(c.Consumer.FetchMaxBytes)
	config.Consumer.MaxWaitTime = c.Consumer.MaxWaitTime

	// 手动提交 offset
	config.Consumer.Offsets.AutoCommit.Enable = false

	return config
}
