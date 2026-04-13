package data

import (
	"context"
	"fmt"
	"github.com/google/wire"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"seckill-service/internal/conf"
	"seckill-service/internal/data/core/query"
	payQuery "seckill-service/internal/data/pay/query"
	productQuery "seckill-service/internal/data/product/query"
	userQuery "seckill-service/internal/data/user/query"
)

var ProviderSet = wire.NewSet(NewData)

// Data .
type Data struct {
	// 四个数据库的连接
	coreDB    *gorm.DB
	userDB    *gorm.DB
	productDB *gorm.DB
	payDB     *gorm.DB

	// Gen 查询对象
	Core    *query.Query
	User    *userQuery.Query
	Product *productQuery.Query
	Pay     *payQuery.Query

	log *log.Helper
}
type CoreQuery interface {
	SeckillSku() interface{}
	SeckillOrder() interface{}
	SeckillActivity() interface{}
	Coupon() interface{}
}

// UserQueryInterface 用户库查询接口
type UserQueryInterface interface {
	User() interface{}
	UserAddress() interface{}
}

// ProductQueryInterface 商品库查询接口
type ProductQueryInterface interface {
	Product() interface{}
	Category() interface{}
}

// PayQueryInterface 支付库查询接口
type PayQueryInterface interface {
	PayInfo() interface{}
}

// NewData .
func NewData(c *conf.Data, logger log.Logger) (*Data, error) {
	logHelper := log.NewHelper(log.With(logger, "module", "data"))

	// gorm配置
	gormConfig := &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
		Logger:                 gormLogger.Default.LogMode(gormLogger.Info),
	}

	// 连接核心库
	coreDB, err := gorm.Open(mysql.Open(c.CoreDb.Source), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("connect user db failed: %w", err)
	}

	// 连接用户库
	userDB, err := gorm.Open(mysql.Open(c.UserDb.Source), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("connect user db failed: %w", err)
	}

	// 连接商品库
	productDB, err := gorm.Open(mysql.Open(c.ProductDb.Source), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("connect product db failed: %w", err)
	}

	// 连接支付库
	payDB, err := gorm.Open(mysql.Open(c.PayDb.Source), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("connect pay db failed: %w", err)
	}

	// 设置连接池
	setPool(coreDB, "core")
	setPool(userDB, "user")
	setPool(productDB, "product")
	setPool(payDB, "pay")

	return &Data{
		coreDB:    coreDB,
		userDB:    userDB,
		productDB: productDB,
		payDB:     payDB,
		Core:      query.Use(coreDB),
		User:      userQuery.Use(userDB),
		Product:   productQuery.Use(productDB),
		Pay:       payQuery.Use(payDB),
		log:       logHelper,
	}, nil
}

func setPool(db *gorm.DB, name string) {
	sqlDB, err := db.DB()
	if err != nil {
		return
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)
}

// GetCoreQuery 获取核心库查询对象（支持事务）
func (d *Data) GetCoreQuery(ctx context.Context) *query.Query {
	if tx, ok := ctx.Value("tx").(*gorm.DB); ok {
		// 事务中返回新的查询对象
		return query.Use(tx)
	}
	// 非事务中返回原始查询对象
	return d.Core
}

// GetCoreQueryForTx 用于事务中的查询（返回带上下文的查询）
func (d *Data) GetCoreQueryForTx(ctx context.Context) interface{} {
	if tx, ok := ctx.Value("tx").(*gorm.DB); ok {
		return query.Use(tx)
	}
	return d.Core.WithContext(ctx)
}

// GetUserQuery 获取用户库查询对象
func (d *Data) GetUserQuery(ctx context.Context) *userQuery.Query {
	// 直接返回原始查询对象，不添加上下文
	return d.User
}

// GetUserQueryWithContext 获取带上下文的用户库查询对象
func (d *Data) GetUserQueryWithContext(ctx context.Context) interface{} {
	return d.User.WithContext(ctx)
}

// GetProductQuery 获取商品库查询对象
func (d *Data) GetProductQuery(ctx context.Context) *productQuery.Query {
	return d.Product
}

// GetProductQueryWithContext 获取带上下文的商品库查询对象
func (d *Data) GetProductQueryWithContext(ctx context.Context) interface{} {
	return d.Product.WithContext(ctx)
}

// GetPayQuery 获取支付库查询对象
func (d *Data) GetPayQuery(ctx context.Context) *payQuery.Query {
	return d.Pay
}

// GetPayQueryWithContext 获取带上下文的支付库查询对象
func (d *Data) GetPayQueryWithContext(ctx context.Context) interface{} {
	return d.Pay.WithContext(ctx)
}
