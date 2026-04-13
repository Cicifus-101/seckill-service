package mq

// SeckillOrderMessage 秒杀订单消息
type SeckillOrderMessage struct {
	OrderNo        string `json:"order_no"`
	RequestId      string `json:"request_id"`
	TraceID        string `json:"trace_id"` // 链路追踪ID
	UserID         uint64 `json:"user_id"`
	SkuID          uint64 `json:"sku_id"`
	ActivityID     uint64 `json:"activity_id"`
	ProductID      uint64 `json:"product_id"`
	Quantity       int    `json:"quantity"`
	AddressID      uint64 `json:"address_id"`
	CouponID       uint64 `json:"coupon_id"`
	OrderAmount    uint64 `json:"order_amount"`
	CouponDiscount uint64 `json:"coupon_discount"`
	FinalAmount    uint64 `json:"final_amount"`
	SeckillPrice   uint64 `json:"seckill_price"` //单位：分
	Version        uint32 `json:"version"`
	Timestamp      int64  `json:"timestamp"`
}

// SeckillResultMessage 订单结果消息
type SeckillResultMessage struct {
	OrderNo    string `json:"order_no"`
	UserID     uint64 `json:"user_id"`
	Status     int    `json:"status"`
	FailReason string `json:"fail_reason"`
	Timestamp  int64  `json:"timestamp"`
}

// DeadLetterMessage 死信消息
type DeadLetterMessage struct {
	OriginalMsg SeckillOrderMessage `json:"original_msg"`
	LastError   string              `json:"last_error"`
	RetryCount  int                 `json:"retry_count"`
	NextRetryAt int64               `json:"next_retry_at"`
}

func (d *DeadLetterMessage) IsReadyToRetry(now int64) bool {
	return now >= d.NextRetryAt
}
