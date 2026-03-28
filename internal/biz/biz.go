package biz

import (
	"errors"
	"github.com/google/wire"
)

// ProviderSet is biz providers.
var ProviderSet = wire.NewSet(NewSeckillUsecase)

// 系统级错误
var (
	ErrSystemBusy       = errors.New("系统繁忙，请稍后再试")
	ErrTooManyRequests  = errors.New("请求过于频繁，请稍后再试")
	ErrDuplicateRequest = errors.New("重复请求，请勿重复提交")
)

// 活动相关错误
var (
	ErrNoActiveActivity = errors.New("暂无进行中的秒杀活动")
	ErrActivityNotStart = errors.New("活动尚未开始")
	ErrActivityEnded    = errors.New("活动已结束")
)

// 商品相关错误
var (
	ErrProductNotFound     = errors.New("商品不存在")
	ErrInsufficientStock   = errors.New("库存不足")
	ErrExceedLimit         = errors.New("超过限购数量")
	ErrStockUpdateConflict = errors.New("库存更新冲突")
)

// 订单相关错误
var (
	ErrOrderNotFound        = errors.New("订单不存在")
	ErrOrderStatusIncorrect = errors.New("订单状态不正确")
	ErrOrderTimeout         = errors.New("订单已超时，请重新下单")
	ErrOrderExists          = errors.New("订单已存在")
)

// 用户相关错误
var (
	ErrUserNotMatch    = errors.New("用户不匹配")
	ErrAddressNotFound = errors.New("收货地址不存在")
	ErrAlreadyBought   = errors.New("您已购买过该商品，不可重复购买")
)

// 优惠券相关错误
var (
	ErrCouponInvalid  = errors.New("优惠券无效或已过期")
	ErrCouponUsed     = errors.New("优惠券已被使用")
	ErrCouponNotMatch = errors.New("不满足优惠券使用条件")
)
