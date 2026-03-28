package mq

// SeckillOrderMessage 秒杀订单消息
type SeckillOrderMessage struct {
	OrderNo      string `json:"order_no"`
	UserID       uint64 `json:"user_id"`
	SkuID        uint64 `json:"sku_id"`
	ActivityID   uint64 `json:"activity_id"`
	ProductID    uint64 `json:"product_id"`
	Quantity     int    `json:"quantity"`
	AddressID    uint64 `json:"address_id"`
	CouponID     uint64 `json:"coupon_id"`
	SeckillPrice uint64 `json:"seckill_price"`
	Version      uint32 `json:"version"`
	Timestamp    int64  `json:"timestamp"`
}

// SeckillResultMessage 订单结果消息
type SeckillResultMessage struct {
	OrderNo    string `json:"order_no"`
	UserID     uint64 `json:"user_id"`
	Status     int    `json:"status"`
	FailReason string `json:"fail_reason"`
}
