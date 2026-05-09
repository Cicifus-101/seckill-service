package model

import "time"

const TableNameUserCoupon = "user_coupon"

// UserCoupon 用户领取的优惠券实例。
type UserCoupon struct {
	ID             uint64     `gorm:"column:id;type:bigint unsigned;primaryKey;autoIncrement:true" json:"id"`
	UserID         uint64     `gorm:"column:user_id;type:bigint unsigned;not null;index:idx_user_status,priority:1" json:"user_id"`
	CouponID       uint64     `gorm:"column:coupon_id;type:bigint unsigned;not null;index:idx_coupon_id" json:"coupon_id"`
	Scene          string     `gorm:"column:scene;type:varchar(32);not null;default:'';comment:发券场景" json:"scene"`
	SourceType     string     `gorm:"column:source_type;type:varchar(32);not null;default:'';comment:来源类型" json:"source_type"`
	SourceID       string     `gorm:"column:source_id;type:varchar(64);not null;default:'';comment:来源ID" json:"source_id"`
	IdempotencyKey string     `gorm:"column:idempotency_key;type:varchar(128);not null;uniqueIndex:uk_idempotency_key" json:"idempotency_key"`
	Status         int32      `gorm:"column:status;type:tinyint;not null;default:1;index:idx_user_status,priority:2;comment:1-未使用 2-已使用 3-已过期" json:"status"`
	ReceivedTime   time.Time  `gorm:"column:received_time;type:datetime;not null;default:CURRENT_TIMESTAMP" json:"received_time"`
	UsedTime       *time.Time `gorm:"column:used_time;type:datetime" json:"used_time"`
	ExpireTime     time.Time  `gorm:"column:expire_time;type:datetime;not null;index:idx_expire_time" json:"expire_time"`
	CreateTime     *time.Time `gorm:"column:create_time;type:datetime;not null;default:CURRENT_TIMESTAMP" json:"create_time"`
	UpdateTime     *time.Time `gorm:"column:update_time;type:datetime;not null;default:CURRENT_TIMESTAMP" json:"update_time"`

	Coupon *Coupon `gorm:"foreignKey:CouponID;references:ID" json:"coupon,omitempty"`
}

func (*UserCoupon) TableName() string {
	return TableNameUserCoupon
}
