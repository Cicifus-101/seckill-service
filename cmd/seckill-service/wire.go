//go:build wireinject
// +build wireinject

// The build tag makes sure the stub is not built in the final build.

package main

import (
	"context"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/wire"

	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/go-kratos/kratos/v2/transport/http"
	internalprovider "seckill-service/internal"
	"seckill-service/internal/biz"
	"seckill-service/internal/conf"
	"seckill-service/internal/job"
	"seckill-service/internal/kafka"
	"seckill-service/internal/server"
)

// wireApp init kratos application.
func wireApp(s *conf.Server, d *conf.Data, k *conf.Kafka, logger log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		provideRedis,
		provideKafkaConfig,
		server.ProviderSet,
		internalprovider.ProviderSet,
		NewBackgroundTasks,
		newApp,
	))
}

// provideRedis 提供 Redis 客户端
func provideRedis(c *conf.Data) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     c.Redis.Addr,
		Password: c.Redis.Password,
		DB:       int(c.Redis.Db),
	})
}

func provideKafkaConfig(k *conf.Kafka) *kafka.Config {
	return kafka.FromProto(k)
}

// BackgroundTasks 后台任务启动器
type BackgroundTasks struct {
	Consumer       *kafka.Consumer
	DelayQueue     *job.DelayQueue
	CompensateTask *job.CompensateTask
	SeckillUsecase *biz.SeckillUsecase
}

// NewBackgroundTasks 创建后台任务启动器
func NewBackgroundTasks(
	consumer *kafka.Consumer,
	delayQueue *job.DelayQueue,
	compensateTask *job.CompensateTask,
	seckillUsecase *biz.SeckillUsecase,
) *BackgroundTasks {
	return &BackgroundTasks{
		Consumer:       consumer,
		DelayQueue:     delayQueue,
		CompensateTask: compensateTask,
		SeckillUsecase: seckillUsecase,
	}
}

// Start 启动所有后台任务
func (b *BackgroundTasks) Start(ctx context.Context, logger log.Logger) {
	helper := log.NewHelper(logger)

	// 预热缓存
	helper.Info("开始预热缓存...")
	if err := b.SeckillUsecase.WarmUpSeckillCache(ctx, 1); err != nil {
		helper.Warnf("预热缓存失败: %v", err)
	} else {
		helper.Info("缓存预热完成")
	}

	// 启动 Kafka 主消费者
	helper.Info("启动 Kafka 主消费者...")
	if err := b.Consumer.Start(ctx); err != nil {
		helper.Errorf("启动 Kafka 消费者失败: %v", err)
	}

	// 启动延迟队列（订单超时取消）
	helper.Info("启动延迟队列...")
	go b.DelayQueue.Start(ctx)

	// 启动补偿任务
	helper.Info("启动补偿任务...")
	go b.CompensateTask.Start(ctx)

	helper.Info("所有后台任务启动成功")
}

// Stop 停止所有后台任务
func (b *BackgroundTasks) Stop(ctx context.Context, logger log.Logger) {
	helper := log.NewHelper(logger)
	helper.Info("正在停止后台任务...")

	// 停止 Kafka 消费者
	if err := b.Consumer.Stop(); err != nil {
		helper.Errorf("停止 Kafka 消费者失败: %v", err)
	}

	helper.Info("所有后台任务已停止")
}

func newApp(logger log.Logger, gs *grpc.Server, hs *http.Server, bg *BackgroundTasks) *kratos.App {
	return kratos.New(
		kratos.ID("seckill-service"),
		kratos.Name("seckill-service"),
		kratos.Version("1.0.0"),
		kratos.Metadata(map[string]string{}),
		kratos.Logger(logger),
		kratos.Server(gs, hs),
		kratos.AfterStart(func(ctx context.Context) error {
			bg.Start(ctx, logger)
			return nil
		}),
		kratos.BeforeStop(func(ctx context.Context) error {
			bg.Stop(ctx, logger)
			return nil
		}),
	)
}
