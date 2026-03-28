package kafka

import (
	"context"
	"encoding/json"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"seckill-service/internal/mq"
	"time"
)

type Producer struct {
	rdb *redis.Client
	log *log.Helper
}

func NewProducer(rdb *redis.Client, logger log.Logger) *Producer {
	return &Producer{
		rdb: rdb,
		log: log.NewHelper(log.With(logger, "module", "mq/producer")),
	}
}

// Send 发送订单消息
func (p *Producer) Send(ctx context.Context, msg *mq.SeckillOrderMessage) error {
	msg.Timestamp = time.Now().Unix()
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	return p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: "seckill:orders",
		Values: map[string]interface{}{"data": data},
	}).Err()
}
