package data

import (
	"context"
	"gorm.io/gorm"
	"seckill-service/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
)

// Transaction 事务接口
type Transaction interface {
	ExecTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type transaction struct {
	data *Data
	log  *log.Helper
}

func NewTransaction(data *Data, logger log.Logger) biz.Transaction {
	return &transaction{
		data: data,
		log:  log.NewHelper(log.With(logger, "module", "data/tx")),
	}
}

func (t *transaction) ExecTx(ctx context.Context, fn func(ctx context.Context) error) error {
	// 核心库事务
	return t.data.coreDB.Transaction(func(tx *gorm.DB) error {
		ctx = context.WithValue(ctx, "tx", tx)
		return fn(ctx)
	})
}
