package biz

import "time"

// SeckillProduct 秒杀商品列表项
type SeckillProduct struct {
	SkuID          uint64
	ActivityID     uint64
	ProductID      uint64
	Name           string
	MainImage      string
	SeckillPrice   uint64
	MarketPrice    uint64
	AvailableStock int64
	TotalStock     int64
	LimitNum       int64
	SaleRate       int64
	UserHasBought  bool
}

// SeckillProductDetail 秒杀商品详情
type SeckillProductDetail struct {
	ProductID        uint64
	Name             string
	Subtitle         string
	MainImage        string
	Detail           string
	SkuID            uint64
	SeckillPrice     uint64
	MarketPrice      uint64
	TotalStock       int64
	AvailableStock   int64
	LimitNum         int64
	ActivityID       uint64
	ActivityTitle    string
	Description      string
	StartTime        string
	EndTime          string
	ActivityStatus   int64
	RemainingSeconds int64
	Version          uint32
}

// Activity 活动信息
type Activity struct {
	ID               uint64
	Title            string
	Description      string
	StartTime        string
	EndTime          string
	Status           int32
	RemainingSeconds int64
}

// UserBuyRecord 用户购买记录
type UserBuyRecord struct {
	HasBought bool
	Quantity  int64
}

// UserSeckillStatus 用户秒杀状态
type UserSeckillStatus struct {
	HasBought      bool
	BoughtQuantity int64
	CanBuy         bool
	RemainingLimit int64
	Message        string
}

// Address 收货地址
type Address struct {
	ID            uint64
	ReceiverName  string
	ReceiverPhone string
	Province      string
	City          string
	District      string
	DetailAddress string
	IsDefault     bool
}

// AddressSnapshot 地址快照（用于订单）
type AddressSnapshot struct {
	ReceiverName  string
	ReceiverPhone string
	Province      string
	City          string
	District      string
	DetailAddress string
}

// Coupon 优惠券信息
type Coupon struct {
	ID        uint64
	Name      string
	Type      int32 // 1-满减 2-打折
	Value     uint64
	MinAmount uint64
	Version   uint32
}

type UserCoupon struct {
	ID             uint64
	UserID         uint64
	CouponID       uint64
	Name           string
	Type           int32
	Value          uint64
	MinAmount      uint64
	Scene          string
	SourceType     string
	SourceID       string
	IdempotencyKey string
	Status         int32
	ReceivedTime   string
	UsedTime       string
	ExpireTime     string
	Version        uint32
}

// Order 创建订单参数
type Order struct {
	OrderNo        string
	UserID         uint64
	RequestID      string
	ActivityID     uint64
	ProductID      uint64
	SkuID          uint64
	ProductName    string
	ProductImage   string
	SeckillPrice   uint64
	Quantity       int64
	OrderAmount    uint64
	CouponID       uint64
	CouponDiscount uint64
	FinalAmount    uint64
	AddressID      uint64
	Status         OrderStatus
}

type OrderStatus int

// OrderInfo 订单信息
type OrderInfo struct {
	OrderNo          string
	UserID           uint64
	ActivityID       uint64
	ProductID        uint64
	SkuID            uint64
	ProductName      string
	ProductImage     string
	SeckillPrice     uint64
	Quantity         int64
	OrderAmount      uint64
	CouponID         uint64
	CouponDiscount   uint64
	FinalAmount      uint64
	Status           int32
	CreateTime       string
	PayTime          string
	Address          *AddressSnapshot
	RemainingSeconds int64
}

// PayInfo 支付信息
type PayInfo struct {
	OrderNo        string
	UserID         uint64
	PayPlatform    int32
	PlatformNumber string
	PlatformStatus string
	PayAmount      uint64
	PayTime        *time.Time
}

type PendingReservation struct {
	OrderNo    string `json:"order_no"`
	RequestID  string `json:"request_id"`
	UserID     uint64 `json:"user_id"`
	ActivityID uint64 `json:"activity_id"`
	SkuID      uint64 `json:"sku_id"`
	Quantity   int    `json:"quantity"`
	CouponID   uint64 `json:"coupon_id"`
	CreatedAt  int64  `json:"created_at"`
}

type DeadLetterMessage struct {
	ID           uint64
	EventID      string
	Topic        string
	Partition    int32
	Offset       int64
	OrderNo      string
	RequestID    string
	TraceID      string
	RetryCount   int
	ErrorMessage string
	RawPayload   string
	Status       string
	CreateTime   time.Time
	UpdateTime   time.Time
}

const (
	DeadLetterStatusPending  = "PENDING"
	DeadLetterStatusReplayed = "REPLAYED"
	DeadLetterStatusFailed   = "FAILED"
)
