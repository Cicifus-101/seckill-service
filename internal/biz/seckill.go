package biz

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"
	"time"
)

const (
	OrderStatusPending = 0 // 待支付
	OrderStatusPaid    = 1 // 已支付
	OrderStatusCancel  = 2 // 已取消
	OrderStatusRefund  = 3 // 已退款
)

const (
	OrderTimeoutMinutes = 15                       // 订单超时时间（分钟）
	OrderTimeoutSeconds = OrderTimeoutMinutes * 60 // 订单超时秒数
)

// 秒杀存储接口
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

// Transaction 事务接口
type Transaction interface {
	ExecTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// SkuStock SKU库存信息
type SkuStock struct {
	ID           uint64
	SeckillPrice uint64
	Version      uint32
}

type SeckillUsecase struct {
	Repo SeckillRepo
	log  *log.Helper
	tx   Transaction
}

func NewSeckillUsecase(repo SeckillRepo, logger log.Logger, tx Transaction) *SeckillUsecase {
	return &SeckillUsecase{
		Repo: repo,
		log:  log.NewHelper(log.With(logger, "module", "usecase/seckill")),
		tx:   tx,
	}
}

// ListSeckillProducts 获取秒杀商品列表
func (uc *SeckillUsecase) ListSeckillProducts(ctx context.Context, userID, activityID int64, page, pageSize, sortType int32) (*SeckillProductsResult, error) {
	// 如果没有指定活动ID，获取当前活动
	if activityID == 0 {
		activity, _, err := uc.Repo.GetCurrentActivity(ctx)
		if err != nil && !errors.Is(err, ErrNoActiveActivity) {
			return nil, err
		}
		if activity != nil {
			activityID = int64(activity.ID)
		}
	}

	// 查询商品列表
	products, total, err := uc.Repo.ListSeckillProducts(ctx, uint64(activityID), page, pageSize, sortType)
	if err != nil {
		return nil, err
	}

	// 查询购买状态
	if userID > 0 && len(products) > 0 {
		for _, p := range products {
			record, err := uc.Repo.CheckUserBuyRecord(ctx, uint64(userID), uint64(activityID), p.ProductID)
			if err == nil {
				p.UserHasBought = record.HasBought
			}
		}
	}

	// 获取活动信息
	var activityInfo *Activity //初始值为nil(空指针)
	if activityID > 0 {
		activity, _, err := uc.Repo.GetCurrentActivity(ctx)
		if err == nil && activity != nil && activity.ID == uint64(activityID) {
			activityInfo = activity
		}
	}

	return &SeckillProductsResult{
		Products: products,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Activity: activityInfo,
	}, nil
}

// GetSeckillProductDetail 获取秒杀商品详情
func (uc *SeckillUsecase) GetSeckillProductDetail(ctx context.Context, userID, productID, activityID uint64) (*ProductDetailResult, error) {
	// 获取商品详情
	detail, err := uc.Repo.GetSeckillProductDetail(ctx, productID, activityID)
	if err != nil {
		return nil, err
	}

	res := &ProductDetailResult{
		Product:          detail,
		AvailableCoupons: []*CouponInfo{},
	}

	// 如果已经登录，获取秒杀状态
	if userID > 0 {
		record, err := uc.Repo.CheckUserBuyRecord(ctx, userID, activityID, productID)
		if err == nil {
			status := &UserSeckillStatus{
				HasBought:      record.HasBought,
				BoughtQuantity: record.Quantity,
			}

			// 判断是否可购买
			if detail.ActivityStatus == 0 {
				status.CanBuy = false
				status.Message = "活动未开始"
			} else if detail.ActivityStatus == 2 {
				status.CanBuy = false
				status.Message = "活动已结束"
			} else if detail.AvailableStock <= 0 {
				status.CanBuy = false
				status.Message = "商品已售罄"
			} else if record.HasBought && record.Quantity >= detail.LimitNum {
				status.CanBuy = false
				status.Message = "超过限购数量"
			} else {
				status.CanBuy = true
				status.RemainingLimit = detail.LimitNum - record.Quantity
				status.Message = "可购买"
			}
			res.UserStatus = status
		}
	}

	return res, nil
}

// CreateSeckillOrder 创建秒杀订单
func (uc *SeckillUsecase) CreateSeckillOrder(ctx context.Context, req *CreateOrderRequest) (*CreateOrderResult, error) {
	var res *CreateOrderResult

	// 使用事务保持数据的一致性
	err := uc.tx.ExecTx(ctx, func(txCtx context.Context) error {
		// 1.检查活动状态
		activity, _, err := uc.Repo.GetCurrentActivity(ctx)
		if err != nil {
			return err
		}
		if activity.ID != req.ActivityID {
			return ErrNoActiveActivity
		}

		// 2.检查用户的购买记录（实现一人一单）
		record, err := uc.Repo.CheckUserBuyRecord(txCtx, req.UserID, req.ActivityID, req.ProductID)
		if err != nil {
			return err
		}

		productInfo, err := uc.Repo.GetSeckillProductDetail(txCtx, req.ProductID, req.ActivityID)
		if err != nil {
			return err
		}

		if record.Quantity+req.Quantity > productInfo.LimitNum {
			return ErrExceedLimit
		}

		// 3.锁定库存 （使用SELECT FOR UPDATE）
		skuStock, err := uc.Repo.LockStock(txCtx, req.SkuID, uint32(req.Quantity))
		if err != nil {
			return err
		}

		// 4.计算订单金额
		orderAmount := skuStock.SeckillPrice * uint64(req.Quantity)
		finalAmount, couponDiscount, err := uc.applyCoupon(txCtx, req.CouponID, orderAmount)
		if err != nil {
			return err
		}

		// 5.获取收货地址
		address, err := uc.Repo.GetUserAddress(txCtx, req.AddressID)
		if err != nil {
			return err
		}
		// 6.创建订单
		order := &Order{
			UserID:       req.UserID,
			ActivityID:   req.ActivityID,
			ProductID:    req.ProductID,
			SkuID:        skuStock.ID,
			ProductName:  productInfo.Name,
			ProductImage: productInfo.MainImage,
			SeckillPrice: skuStock.SeckillPrice,
			Quantity:     req.Quantity,
			OrderAmount:  orderAmount,
			CouponID:     req.CouponID,
			AddressID:    req.AddressID,
		}

		orderNo, err := uc.Repo.CreateOrder(txCtx, order)
		if err != nil {
			return err
		}

		// 7.创建收货信息快照
		if err := uc.Repo.CreateOrderShipping(txCtx, orderNo, address); err != nil {
			return err
		}

		// 8.扣减库存（使用乐观锁版本）
		if err := uc.Repo.DecreaseStock(txCtx, skuStock.ID, uint32(req.Quantity), skuStock.Version); err != nil {
			return err
		}

		res = &CreateOrderResult{
			OrderNo:          orderNo,
			OrderAmount:      orderAmount,
			CouponDiscount:   couponDiscount,
			FinalAmount:      finalAmount,
			Status:           OrderStatusPending, // 待支付
			SeckillPrice:     skuStock.SeckillPrice,
			Quantity:         req.Quantity,
			RemainingSeconds: OrderTimeoutSeconds, // 15分钟支付倒计时
		}
		return nil
	})

	if err != nil {
		uc.log.WithContext(ctx).Errorf("创建订单失败: %v", err)
		return nil, err
	}

	return res, nil
}

// GetSeckillOrder 获取订单信息
func (uc *SeckillUsecase) GetSeckillOrder(ctx context.Context, orderNo string, userID uint64) (*OrderInfo, error) {
	order, err := uc.Repo.GetOrder(ctx, orderNo)
	if err != nil {
		return nil, err
	}

	// 鉴权
	if order.UserID != userID {
		return nil, ErrUserNotMatch
	}

	return order, nil
}

// PaySeckillOrder 支付订单
func (uc *SeckillUsecase) PaySeckillOrder(ctx context.Context, req *PayOrderRequest) (*PayOrderResult, error) {
	var result *PayOrderResult
	var isTimeout bool

	err := uc.tx.ExecTx(ctx, func(txctx context.Context) error {
		// 1.获取订单信息
		order, err := uc.Repo.GetOrderForUpdate(txctx, req.OrderNo)
		if err != nil {
			return err
		}

		if order.UserID != req.UserID {
			return ErrUserNotMatch
		}

		// 2.检查订单状态（防止重复支付、以及对已处理订单进行操作）
		if order.Status != OrderStatusPending {
			return ErrOrderStatusIncorrect
		}

		// 3.检查支付超时（支付时用户主动进行超时检查）
		// 4. 超时校验
		if uc.isOrderTimeout(order.CreateTime) {
			isTimeout = true
			return ErrOrderTimeout
		}

		// 5.创建支付记录
		now := time.Now()
		payInfo := &PayInfo{
			OrderNo:        req.OrderNo,
			UserID:         req.UserID,
			PayPlatform:    req.PayPlatform,
			PlatformNumber: uc.generatePlatformNumber(req.UserID),
			PlatformStatus: "SUCCESS",
			PayAmount:      order.FinalAmount,
			PayTime:        &now,
		}

		if err := uc.Repo.CreatePayInfo(txctx, payInfo); err != nil {
			return fmt.Errorf("创建支付记录失败: %w", err)
		}

		// 6. 更新订单状态
		if err := uc.Repo.UpdateOrderStatus(txctx, req.OrderNo, OrderStatusPaid); err != nil {
			return err
		}

		result = &PayOrderResult{
			Success:        true,
			PayAmount:      order.FinalAmount,
			PlatformNumber: payInfo.PlatformNumber,
		}
		return nil
	})

	if err != nil && errors.Is(err, ErrOrderTimeout) && isTimeout {
		if cancelErr := uc.cancelOrder(ctx, req.OrderNo, "支付超时"); cancelErr != nil {
			uc.log.WithContext(ctx).Errorf("取消超时订单失败: %v", cancelErr)
		}
		return nil, ErrOrderTimeout
	}

	return result, nil
}

// CancelOrder 取消订单
func (uc *SeckillUsecase) CancelOrder(ctx context.Context, orderNo string, userID uint64, reason string) error {
	// 获取订单信息验证权限
	order, err := uc.Repo.GetOrder(ctx, orderNo)
	if err != nil {
		return err
	}
	if order.UserID != userID {
		return ErrUserNotMatch
	}

	return uc.cancelOrder(ctx, orderNo, reason)
}

// CancelTimeoutOrders 定时取消超时订单
func (uc *SeckillUsecase) CancelTimeoutOrders(ctx context.Context) error {
	orders, err := uc.Repo.GetPendingOrders(ctx, OrderTimeoutMinutes)
	if err != nil {
		return err
	}

	if len(orders) == 0 {
		return nil
	}

	uc.log.WithContext(ctx).Infof("发现 %d 个超时订单", len(orders))
	success, fail := 0, 0
	for _, order := range orders {
		if err := uc.cancelOrder(ctx, order.OrderNo, "定时任务取消"); err != nil {
			fail++
			uc.log.WithContext(ctx).Errorf("取消订单失败: %s, err=%v", order.OrderNo, err)
		} else {
			success++
		}
	}

	uc.log.WithContext(ctx).Infof("超时订单处理完成: 成功=%d, 失败=%d", success, fail)
	return nil
}

// cancelOrder 内部取消订单
func (uc *SeckillUsecase) cancelOrder(ctx context.Context, orderNo string, reason string) error {
	return uc.tx.ExecTx(ctx, func(txCtx context.Context) error {
		// 1.获取订单信息
		order, err := uc.Repo.GetOrderForUpdate(txCtx, orderNo)
		if err != nil {
			return fmt.Errorf("获取订单失败: %w", err)
		}
		// 2.检查订单状态（只取消待支付订单）
		if order.Status != OrderStatusPending {
			uc.log.WithContext(txCtx).Warnf("订单状态不是待支付，无法取消: %s, status=%d", orderNo, order.Status)
			return nil
		}

		// 3.更新订单状态为已取消
		if err := uc.Repo.UpdateOrderStatus(txCtx, orderNo, OrderStatusCancel); err != nil {
			return fmt.Errorf("更新订单状态失败: %w", err)
		}

		// 4.恢复库存
		if err := uc.Repo.RestoreStock(txCtx, order.SkuID, uint32(order.Quantity)); err != nil {
			uc.log.WithContext(txCtx).Errorf("恢复库存失败: orderNo=%s, skuID=%d, quantity=%d, err=%v",
				orderNo, order.SkuID, order.Quantity, err)
		}

		// 5.恢复优惠券（如果使用优惠券）
		if order.CouponID > 0 {
			if err := uc.Repo.RestoreCoupon(txCtx, order.CouponID); err != nil {
				uc.log.WithContext(txCtx).Warnf("恢复优惠券失败: orderNo=%s, couponID=%d, err=%v",
					orderNo, order.CouponID, err)
			}
		}
		uc.log.WithContext(ctx).Infof("取消订单成功: %s, 原因: %s", orderNo, reason)
		return nil
	})
}

// GetSeckillResult 获取秒杀结果（用于轮询）
func (uc *SeckillUsecase) GetSeckillResult(ctx context.Context, userID uint64, requestID string) (*SeckillResult, error) {
	// 这里可以通过Redis或数据库查询秒杀结果
	// V1版本简单返回处理中
	return &SeckillResult{
		Status:  0, // 处理中
		Message: "处理中",
	}, nil
}

// applyCoupon 应用优惠券，返回最终金额和优惠金额
func (uc *SeckillUsecase) applyCoupon(ctx context.Context, couponID uint64, orderAmount uint64) (finalAmount, discount uint64, err error) {
	if couponID == 0 {
		return orderAmount, 0, nil
	}

	coupon, err := uc.Repo.GetCoupon(ctx, couponID)
	if err != nil {
		return 0, 0, err
	}

	if orderAmount < coupon.MinAmount {
		return 0, 0, errors.New("未达到优惠券使用门槛")
	}

	switch coupon.Type {
	case 1: // 满减
		discount = coupon.Value
		if discount > orderAmount {
			discount = orderAmount
		}
	case 2: // 打折
		discount = orderAmount * (100 - coupon.Value) / 100
	default:
		return 0, 0, errors.New("无效的优惠券类型")
	}

	finalAmount = orderAmount - discount

	// 扣减优惠券库存
	if err := uc.Repo.UseCoupon(ctx, coupon.ID, coupon.Version); err != nil {
		return 0, 0, err
	}

	return finalAmount, discount, nil
}

// buildUserStatus 构建用户秒杀状态
func (uc *SeckillUsecase) buildUserStatus(detail *SeckillProductDetail, record *UserBuyRecord) *UserSeckillStatus {
	status := &UserSeckillStatus{
		HasBought:      record.HasBought,
		BoughtQuantity: record.Quantity,
	}

	switch {
	case detail.ActivityStatus == 0:
		status.CanBuy, status.Message = false, "活动未开始"
	case detail.ActivityStatus == 2:
		status.CanBuy, status.Message = false, "活动已结束"
	case detail.AvailableStock <= 0:
		status.CanBuy, status.Message = false, "商品已售罄"
	case record.HasBought && record.Quantity >= detail.LimitNum:
		status.CanBuy, status.Message = false, "超过限购数量"
	default:
		status.CanBuy = true
		status.RemainingLimit = detail.LimitNum - record.Quantity
		if status.RemainingLimit < 0 {
			status.RemainingLimit = 0
		}
		status.Message = "可购买"
	}
	return status
}

// isOrderTimeout 检查订单是否超时
func (uc *SeckillUsecase) isOrderTimeout(createTimeStr string) bool {
	createTime, err := time.Parse("2006-01-02 15:04:05", createTimeStr)
	if err != nil {
		return true
	}
	return time.Since(createTime) > OrderTimeoutMinutes*time.Minute
}

// generatePlatformNumber 生成平台流水号
func (uc *SeckillUsecase) generatePlatformNumber(userID uint64) string {
	return fmt.Sprintf("P%d%d", time.Now().UnixNano(), userID%1000)
}
