package biz

import (
	"fmt"
	"github.com/bwmarrin/snowflake"
	"github.com/go-kratos/kratos/v2/log"
	"os"
	"strconv"
)

type IDGenerator interface {
	NextID() int64
	NextString() string
}

type SnowflakeIDGenerator struct {
	node *snowflake.Node
}

func NewIDGenerator(logger log.Logger) IDGenerator {
	helper := log.NewHelper(log.With(logger, "module", "biz/id_generator"))

	workerID := int64(1)
	if v := os.Getenv("SECKILL_WORKER_ID"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			workerID = id
		}
	}

	node, err := snowflake.NewNode(workerID)
	if err != nil {
		helper.Fatalf("init snowflake node failed: %v", err)
	}

	helper.Infof("snowflake id generator initialized, workerID=%d", workerID)

	return &SnowflakeIDGenerator{
		node: node,
	}
}

func (g *SnowflakeIDGenerator) NextID() int64 {
	return g.node.Generate().Int64()
}

func (g *SnowflakeIDGenerator) NextString() string {
	return fmt.Sprintf("%d", g.NextID())
}
