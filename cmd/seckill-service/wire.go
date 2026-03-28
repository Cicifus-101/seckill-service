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
	internalprovider "seckill-service/internal" // 别名避免与包名 "main" 冲突
	"seckill-service/internal/biz"
	"seckill-service/internal/conf"
	"seckill-service/internal/job"
	"seckill-service/internal/kafka"
	"seckill-service/internal/server"
	"seckill-service/internal/service"
)

// wireApp init kratos application.
func wireApp(s *conf.Server, d *conf.Data, logger log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		provideRedis,
		server.ProviderSet,
		internalprovider.ProviderSet,
		service.ProviderSet,
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
	if err := b.SeckillUsecase.WarmUpSeckillCache(ctx, 1); err != nil {
		helper.Warnf("预热缓存失败: %v", err)
	}

	helper.Info("启动 MQ 消费者...")
	go b.Consumer.Start(ctx)

	helper.Info("启动延迟队列...")
	go b.DelayQueue.Start(ctx)

	helper.Info("启动补偿任务...")
	go b.CompensateTask.Start(ctx)
}

func newApp(logger log.Logger, gs *grpc.Server, hs *http.Server, bg *BackgroundTasks) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Metadata(map[string]string{}),
		kratos.Logger(logger),
		kratos.Server(gs, hs),
		kratos.AfterStart(func(ctx context.Context) error {
			bg.Start(ctx, logger)
			return nil
		}),
	)
}
