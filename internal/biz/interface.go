package biz

import (
	"context"
	"seckill-service/internal/mq"
	"time"
)

// SeckillRepo 秒杀接口(Mysql)
type SeckillRepo interface {
	// 商品
	ListSeckillProducts(ctx context.Context, activityID uint64, page, pageSize int32, sortType int32) ([]*SeckillProduct, int64, error)
	GetSeckillProductDetail(ctx context.Context, productID, activityID uint64) (*SeckillProductDetail, error)

	// 活动
	GetCurrentActivity(ctx context.Context) (*Activity, int64, error)

	// 用户
	CheckUserBuyRecord(ctx context.Context, userID, activityID, productID uint64) (*UserBuyRecord, error)
	GetUserAddress(ctx context.Context, addressID uint64) (*Address, error)

	// 库存相关
	LockStock(ctx context.Context, skuID uint64, quantity uint32) (*SkuStock, error)        // 行锁
	DecreaseStock(ctx context.Context, skuID uint64, quantity uint32, version uint32) error // 乐观锁
	RestoreStock(ctx context.Context, skuID uint64, quantity uint32) error                  // 恢复库存

	// 订单相关
	CreateOrder(ctx context.Context, order *Order) (string, error)
	CreateOrderShipping(ctx context.Context, orderNo string, address *Address) error
	GetOrder(ctx context.Context, orderNo string) (*OrderInfo, error)
	GetOrderForUpdate(ctx context.Context, orderNo string) (*OrderInfo, error) // 带行锁
	UpdateOrderStatus(ctx context.Context, orderNo string, status int32) error
	GetPendingOrders(ctx context.Context, timeoutMinutes int) ([]*OrderInfo, error)

	// 支付相关
	CreatePayInfo(ctx context.Context, payInfo *PayInfo) error

	// 优惠券相关
	GetCoupon(ctx context.Context, couponID uint64) (*Coupon, error)
	UseCoupon(ctx context.Context, couponID uint64, version uint32) error
	RestoreCoupon(ctx context.Context, couponID uint64) error // 恢复优惠券库存
}

// CacheRepo 缓存接口(redis)
type CacheRepo interface {
	// 商品缓存
	BatchSetProducts(ctx context.Context, products []*CachedSeckillProduct) error
	GetProductList(ctx context.Context, activityID int64, page, pageSize, sortType int32) (*SeckillProductsResult, error)
	SetProductList(ctx context.Context, activityID int64, page, pageSize, sortType int32, data *SeckillProductsResult, ttl time.Duration) error
	GetProductDetail(ctx context.Context, productID, activityID uint64) (*SeckillProductDetail, error)
	SetProductDetail(ctx context.Context, productID, activityID uint64, detail *SeckillProductDetail, ttl time.Duration) error

	// 库存缓存
	GetStock(ctx context.Context, skuID uint64) (int64, error)
	SetStock(ctx context.Context, skuID uint64, stock int64) error
	DeductStock(ctx context.Context, skuID, userID uint64, quantity int) (int, error)
	RollbackStock(ctx context.Context, skuID uint64, quantity int) error // 下单失败、订单超时

	// 活动缓存
	GetCurrentActivity(ctx context.Context) (*Activity, error)
	SetCurrentActivity(ctx context.Context, activity *Activity, ttl time.Duration) error

	// 用户购买记录
	CheckUserBuy(ctx context.Context, skuID, userID uint64) (bool, error)
	MarkUserBuy(ctx context.Context, skuID, userID uint64, ttl int64) error
	RemoveUserBuy(ctx context.Context, skuID, userID uint64) error

	// 分布式锁（防止击穿）
	SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error)

	// 通用缓存方法
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, key ...string) error
}

// CachedSeckillProduct 缓存的秒杀商品
type CachedSeckillProduct struct {
	SkuID          uint64 `json:"sku_id"`
	ProductID      uint64 `json:"product_id"`
	ActivityID     uint64 `json:"activity_id"`
	Name           string `json:"name"`
	MainImage      string `json:"main_image"`
	SeckillPrice   uint64 `json:"seckill_price"`
	MarketPrice    uint64 `json:"market_price"`
	AvailableStock int64  `json:"available_stock"`
	TotalStock     int64  `json:"total_stock"`
	LimitNum       int64  `json:"limit_num"`
	StartTime      string `json:"start_time"`
	EndTime        string `json:"end_time"`
	ActivityStatus int64  `json:"activity_status"`
}

// RateLimiter 限流器接口
type RateLimiter interface {
	UserRateLimit(ctx context.Context, userID uint64, limit, burst int, window time.Duration) (bool, error)
	GlobalRateLimit(ctx context.Context, limit, burst int, window time.Duration) (bool, error)
	ActivityRateLimit(ctx context.Context, activityID uint64, limit, burst int, window time.Duration) (bool, error)
}

// IdempotentChecker 幂等检查器接口
type IdempotentChecker interface {
	CheckAndMark(ctx context.Context, requestId string, ttl time.Duration) (bool, error)
}

// DelayQueue 延迟队列接口
type DelayQueue interface {
	Add(ctx context.Context, orderNo string, delay time.Duration) error
}

// MQProducer 消息队列生产者接口
type MQProducer interface {
	Send(ctx context.Context, msg *mq.SeckillOrderMessage) error
	SendAsync(ctx context.Context, msg *mq.SeckillOrderMessage)
	SendResult(ctx context.Context, result *mq.SeckillResultMessage) error
	SendToRetry(ctx context.Context, msg *mq.SeckillOrderMessage, retryCount int, delay time.Duration) error
	SendToDLQ(ctx context.Context, msg *mq.SeckillOrderMessage, reason string, retryCount int) error
}
