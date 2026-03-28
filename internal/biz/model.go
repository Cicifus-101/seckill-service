package biz

import "time"

// SeckillProduct 秒杀商品列表项
type SeckillProduct struct {
	SkuID          uint64
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

// Order 创建订单参数
type Order struct {
	UserID       uint64
	ActivityID   uint64
	ProductID    uint64
	SkuID        uint64
	ProductName  string
	ProductImage string
	SeckillPrice uint64
	Quantity     int64
	OrderAmount  uint64
	CouponID     uint64
	AddressID    uint64
}

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
