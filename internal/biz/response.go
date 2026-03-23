package biz

// SeckillProductsResult 秒杀商品列表结果
type SeckillProductsResult struct {
	Products []*SeckillProduct
	Total    int64
	Page     int32
	PageSize int32
	Activity *Activity
}

// ProductDetailResult 商品详情结果
type ProductDetailResult struct {
	Product          *SeckillProductDetail
	AvailableCoupons []*CouponInfo
	UserStatus       *UserSeckillStatus
}

// CouponInfo 优惠券简略信息
type CouponInfo struct {
	ID        uint64
	Name      string
	Type      int32
	Value     uint64
	MinAmount uint64
	EndTime   string
	IsUsable  bool
}

// CreateOrderRequest 创建订单请求
type CreateOrderRequest struct {
	UserID     uint64
	SkuID      uint64
	ActivityID uint64
	ProductID  uint64
	Quantity   int64
	CouponID   uint64
	AddressID  uint64
	ClientIP   string
	RequestID  string
}

// CreateOrderResult 创建订单结果
type CreateOrderResult struct {
	OrderNo          string
	OrderAmount      uint64
	CouponDiscount   uint64
	FinalAmount      uint64
	Status           int32
	SeckillPrice     uint64
	Quantity         int64
	Message          string
	RemainingSeconds int64
}

// PayOrderRequest 支付请求
type PayOrderRequest struct {
	OrderNo     string
	UserID      uint64
	PayPlatform int32
}

// PayOrderResult 支付结果
type PayOrderResult struct {
	Success        bool
	PayAmount      uint64
	PlatformNumber string
	Message        string
}

// SeckillResult 秒杀结果
type SeckillResult struct {
	Status      int32 // 0-处理中 1-成功 2-失败
	OrderNo     string
	Message     string
	OrderAmount uint64
}
