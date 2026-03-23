package biz

import (
	"errors"
	"github.com/google/wire"
)

// ProviderSet is biz providers.
var ProviderSet = wire.NewSet(NewSeckillUsecase)

var (
	ErrInsufficientStock    = errors.New("库存不足")
	ErrStockUpdateConflict  = errors.New("库存更新冲突")
	ErrOrderExists          = errors.New("订单已存在，不能重复下单")
	ErrProductNotFound      = errors.New("商品不存在")
	ErrAddressNotFound      = errors.New("收货地址不存在")
	ErrCouponInvalid        = errors.New("优惠券无效或已用完")
	ErrCouponUsed           = errors.New("优惠券已被使用")
	ErrUserNotMatch         = errors.New("无权查看该订单")
	ErrOrderNotFound        = errors.New("订单不存在")
	ErrOrderStatusIncorrect = errors.New("订单状态不正确")
	ErrOrderTimeout         = errors.New("订单超时")
	ErrNoActiveActivity     = errors.New("暂无进行中的活动")
	ErrExceedLimit          = errors.New("超过限购数量")
	ErrActivityNotStarted   = errors.New("活动未开始")
	ErrActivityEnded        = errors.New("活动已结束")
)
