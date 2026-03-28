package internal

import (
	"github.com/google/wire"

	"seckill-service/internal/biz"
	"seckill-service/internal/cache"
	"seckill-service/internal/data"
	"seckill-service/internal/data/repo"
	"seckill-service/internal/job"
	"seckill-service/internal/kafka"
)

// ProviderSet 全局依赖集合
var ProviderSet = wire.NewSet(

	// 数据层（注意：data 包不导入 repo，但这里可以导入）
	data.NewData,
	data.NewTransaction,

	repo.NewMysqlRepo,
	repo.NewRedisRepo,

	// 缓存层
	cache.NewRateLimiter,
	cache.NewIdempotentChecker,

	wire.Bind(new(biz.RateLimiter), new(*cache.RateLimiter)),
	wire.Bind(new(biz.IdempotentChecker), new(*cache.IdempotentChecker)),
	// 消息队列
	kafka.NewProducer,
	kafka.NewConsumer,

	wire.Bind(new(biz.MQProducer), new(*kafka.Producer)),
	// 任务层
	job.NewDelayQueue,
	job.NewCompensateTask,

	wire.Bind(new(biz.DelayQueue), new(*job.DelayQueue)),
	// 业务层
	biz.NewSeckillUsecase,
)
