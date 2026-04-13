package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"

	"seckill-service/internal/mq"
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

const (
	// 缓存Key前缀
	cacheKeyActivity      = "seckill:activity:current"         // 当前活动
	cacheKeyProductList   = "seckill:product:list:%d:%d:%d:%d" // 商品列表: activityID:page:size:sort
	cacheKeyProductDetail = "seckill:product:detail:%d:%d"     // 商品详情: productID:activityID
	cacheKeyUserBuy       = "seckill:user:buy:%d:%d"           // 用户购买标记: userID:skuID
	cacheKeyStock         = "seckill:stock:%d"                 // 库存: skuID
)

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
	Repo       SeckillRepo
	Cache      CacheRepo
	MQ         MQProducer
	Limiter    RateLimiter
	Idempotent IdempotentChecker
	DelayQueue DelayQueue
	log        *log.Helper
	tx         Transaction
}

func NewSeckillUsecase(repo SeckillRepo, cache CacheRepo, mq MQProducer,
	limiter RateLimiter, idempotent IdempotentChecker, delayQueue DelayQueue,
	logger log.Logger, tx Transaction) *SeckillUsecase {
	return &SeckillUsecase{
		Repo:       repo,
		Cache:      cache,
		MQ:         mq,
		Limiter:    limiter,
		Idempotent: idempotent,
		DelayQueue: delayQueue,
		log:        log.NewHelper(log.With(logger, "module", "usecase/seckill")),
		tx:         tx,
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

	// 只对首页和热门也使用缓存，其他直接查询数据库
	useCache := page <= 3 && activityID > 0

	if useCache {
		res, err := uc.Cache.GetProductList(ctx, activityID, page, pageSize, sortType)
		if err == nil && res != nil {
			uc.log.WithContext(ctx).Debugf("从缓存加载商品列表: page=%d", page)
			return res, nil
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

	result := &SeckillProductsResult{
		Products: products,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Activity: activityInfo,
	}

	// 设置缓存
	if useCache && result != nil {
		if err := uc.Cache.SetProductList(ctx, activityID, page, pageSize, sortType, result, 3*time.Minute); err != nil {
			uc.log.WithContext(ctx).Warnf("设置商品列表缓存失败: %v", err)
		}
	}

	return result, nil
}

// GetSeckillProductDetail 获取秒杀商品详情
func (uc *SeckillUsecase) GetSeckillProductDetail(ctx context.Context, userID, productID, activityID uint64) (*ProductDetailResult, error) {
	detail, err := uc.getProductDetailWithMutex(ctx, productID, activityID)
	if err != nil {
		return nil, err
	}

	res := &ProductDetailResult{
		Product:          detail,
		AvailableCoupons: []*CouponInfo{},
	}

	// 如果已经登录，获取秒杀状态
	if userID > 0 {
		// 优先查Redis购买标记
		hasBought, _ := uc.Cache.CheckUserBuy(ctx, detail.SkuID, userID)
		if hasBought {
			res.UserStatus = &UserSeckillStatus{
				HasBought: true,
				CanBuy:    false,
				Message:   "您已参与过该秒杀活动",
			}
		} else {
			// 查数据库确认
			record, err := uc.Repo.CheckUserBuyRecord(ctx, userID, activityID, productID)
			if err == nil {
				res.UserStatus = uc.buildUserStatus(detail, record)
			}
		}
	}
	return res, nil
}

// getProductDetailWithMutex 带互斥锁的缓存获取（防缓存击穿）
func (uc *SeckillUsecase) getProductDetailWithMutex(ctx context.Context, productID, activityID uint64) (*SeckillProductDetail, error) {
	cacheKey := fmt.Sprintf("seckill:product:%d:%d", productID, activityID)

	// 1. 尝试从缓存获取
	detail, err := uc.Cache.GetProductDetail(ctx, productID, activityID)
	if err == nil && detail != nil {
		return detail, nil
	}

	// 2. 检查是否是空值缓存（防穿透）
	cachedData, _ := uc.Cache.Get(ctx, cacheKey)
	if cachedData == "NULL" {
		return nil, ErrProductNotFound
	}

	// 3. 尝试获取分布式锁（防击穿）
	lockKey := fmt.Sprintf("lock:product:%d:%d", productID, activityID)
	locked, err := uc.Cache.SetNX(ctx, lockKey, "1", 5*time.Second)
	if err != nil {
		uc.log.WithContext(ctx).Warnf("获取分布式锁失败: %v", err)
		// 获取锁失败，降级到DB查询
		return uc.Repo.GetSeckillProductDetail(ctx, productID, activityID)
	}

	if locked {
		defer uc.Cache.Del(ctx, lockKey)

		// 双重检查
		detail, err = uc.Cache.GetProductDetail(ctx, productID, activityID)
		if err == nil && detail != nil {
			return detail, nil
		}

		// 查询DB
		detail, err = uc.Repo.GetSeckillProductDetail(ctx, productID, activityID)
		if err != nil {
			if errors.Is(err, ErrProductNotFound) {
				// 缓存空值，防穿透
				uc.Cache.Set(ctx, cacheKey, "NULL", 1*time.Minute)
			}
			return nil, err
		}

		// 回填缓存
		if err := uc.Cache.SetProductDetail(ctx, productID, activityID, detail, 10*time.Minute); err != nil {
			uc.log.WithContext(ctx).Warnf("设置商品详情缓存失败: %v", err)
		}
		return detail, nil
	}

	// 未获取到锁，短暂等待后重试
	time.Sleep(50 * time.Millisecond)
	return uc.getProductDetailWithMutex(ctx, productID, activityID)
}

// CreateSeckillOrder 创建秒杀订单
func (uc *SeckillUsecase) CreateSeckillOrder(ctx context.Context, req *CreateOrderRequest) (*CreateOrderResult, error) {
	// 全局限流
	allowed, err := uc.Limiter.GlobalRateLimit(ctx, 10000, 20000, time.Second)
	if err != nil || !allowed {
		return nil, ErrSystemBusy
	}

	// 用户限流（防止单个用户刷单）
	allowed, err = uc.Limiter.UserRateLimit(ctx, req.UserID, 3, 3, time.Second)
	if err != nil || !allowed {
		return nil, ErrTooManyRequests
	}

	// 2.1 请求幂等检查（防止重复提交）
	isFirst, err := uc.Idempotent.CheckAndMark(ctx, req.RequestID, 5*time.Minute)
	if err != nil || !isFirst {
		return nil, ErrDuplicateRequest
	}

	// 3.1 获取当前活动
	activity, err := uc.getActivityFromCacheWithMutex(ctx)
	if err != nil {
		return nil, err
	}
	if activity == nil || activity.ID != req.ActivityID {
		return nil, ErrNoActiveActivity
	}

	allowed, err = uc.Limiter.ActivityRateLimit(ctx, activity.ID, 8000, 0, time.Second)
	if err != nil || !allowed {
		return nil, ErrSystemBusy
	}

	// 4.1 获取商品详情，检查限购
	productInfo, err := uc.getProductDetailWithMutex(ctx, req.ProductID, req.ActivityID)
	if err != nil {
		return nil, err
	}
	// 4.2 检查用户是否已购买（一人一单）
	hasBought, err := uc.Cache.CheckUserBuy(ctx, req.SkuID, req.UserID)
	if err == nil && hasBought {
		uc.log.WithContext(ctx).Warnf("用户已购买(Redis), user=%d, sku=%d", req.UserID, req.SkuID)
		return nil, ErrAlreadyBought
	}
	record, err := uc.Repo.CheckUserBuyRecord(ctx, req.UserID, req.ActivityID, req.ProductID)
	if err != nil {
		return nil, err
	}
	if record.HasBought {
		uc.Cache.MarkUserBuy(ctx, req.SkuID, req.UserID, 3600)
		return nil, ErrAlreadyBought
	}

	// 限购检查
	if record.Quantity+req.Quantity > int64(productInfo.LimitNum) {
		return nil, ErrExceedLimit
	}

	// 5.1 原子扣减redis库存
	result, err := uc.Cache.DeductStock(ctx, req.SkuID, req.UserID, int(req.Quantity))
	if err != nil {
		uc.log.WithContext(ctx).Errorf("Redis扣库存失败: %v", err)
		return nil, err
	}
	switch result {
	case -1:
		uc.log.WithContext(ctx).Warnf("Redis检测到重复购买, user=%d, sku=%d", req.UserID, req.SkuID)
		return nil, ErrAlreadyBought
	case 0:
		uc.log.WithContext(ctx).Warnf("Redis库存不足或商品不存在, sku=%d", req.SkuID)
		return nil, ErrInsufficientStock
	case 1:
	default:
		uc.log.WithContext(ctx).Errorf("Redis扣库存返回未知结果: %d", result)
		return nil, fmt.Errorf("扣库存失败")
	}

	// 6.1 构造MQ消息（异步下单）
	msg := &mq.SeckillOrderMessage{
		OrderNo:      generateOrderNo(req.UserID),
		UserID:       req.UserID,
		SkuID:        req.SkuID,
		ActivityID:   req.ActivityID,
		ProductID:    req.ProductID,
		Quantity:     int(req.Quantity),
		AddressID:    req.AddressID,
		CouponID:     req.CouponID,
		SeckillPrice: productInfo.SeckillPrice,
		Version:      productInfo.Version,
	}

	// 6.2 发送 MQ 消息
	if err := uc.MQ.Send(ctx, msg); err != nil {
		// 发送失败，回滚 Redis 库存
		uc.log.WithContext(ctx).Errorf("发送MQ消息失败: %v", err)
		uc.Cache.RollbackStock(ctx, req.SkuID, int(req.Quantity))
		return nil, err
	}

	// 6.3 添加延迟队列（用于超时取消）
	if err := uc.DelayQueue.Add(ctx, msg.OrderNo, 15*time.Minute); err != nil {
		uc.log.WithContext(ctx).Errorf("添加延迟队列失败: %v", err)
		// 不影响主流程，仅记录日志
	}

	uc.Cache.MarkUserBuy(ctx, req.SkuID, req.UserID, 3600)
	uc.log.WithContext(ctx).Infof("订单创建请求已接收, orderNo=%s, user=%d", msg.OrderNo, req.UserID)
	return &CreateOrderResult{
		OrderNo:          msg.OrderNo,
		OrderAmount:      productInfo.SeckillPrice * uint64(req.Quantity),
		CouponDiscount:   0,
		FinalAmount:      productInfo.SeckillPrice * uint64(req.Quantity),
		Status:           OrderStatusPending,
		SeckillPrice:     productInfo.SeckillPrice,
		Quantity:         req.Quantity,
		RemainingSeconds: OrderTimeoutSeconds,
		Message:          "排队中，请稍后查询结果",
	}, nil
}

func (uc *SeckillUsecase) getActivityFromCacheWithMutex(ctx context.Context) (*Activity, error) {
	cacheKey := cacheKeyActivity

	// 尝试从缓存获取
	cachedData, err := uc.Cache.Get(ctx, cacheKey)
	if err == nil && cachedData != "" {
		if cachedData == "NULL" {
			return nil, ErrNoActiveActivity
		}
		var activity Activity
		if err := json.Unmarshal([]byte(cachedData), &activity); err == nil {
			return &activity, nil
		}
	}

	// 尝试获取分布式锁
	lockKey := cacheKey + ":lock"
	locked, err := uc.Cache.SetNX(ctx, lockKey, "1", 3*time.Second)
	if err != nil {
		activity, _, err := uc.Repo.GetCurrentActivity(ctx)
		return activity, err
	}

	if locked {
		defer uc.Cache.Del(ctx, lockKey)

		// 双重检查
		cachedData, err = uc.Cache.Get(ctx, cacheKey)
		if err == nil && cachedData != "" && cachedData != "NULL" {
			var activity Activity
			if err := json.Unmarshal([]byte(cachedData), &activity); err == nil {
				return &activity, nil
			}
		}

		// 查DB
		activity, _, err := uc.Repo.GetCurrentActivity(ctx)
		if err != nil {
			if errors.Is(err, ErrNoActiveActivity) {
				uc.Cache.Set(ctx, cacheKey, "NULL", 30*time.Second)
			}
			return nil, err
		}

		// 回填缓存
		if data, err := json.Marshal(activity); err == nil {
			uc.Cache.Set(ctx, cacheKey, string(data), 30*time.Second)
		}
		return activity, nil
	}

	// 未获取到锁，短暂等待后重试
	time.Sleep(30 * time.Millisecond)
	return uc.getActivityFromCacheWithMutex(ctx)
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
		// 1.获取订单信息(行锁)
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

// InvalidateProductCache 主动失效商品缓存（商品信息更新时调用）
func (uc *SeckillUsecase) InvalidateProductCache(ctx context.Context, productID, activityID uint64) error {
	cacheKey := fmt.Sprintf("seckill:product:%d:%d", productID, activityID)
	if err := uc.Cache.Del(ctx, cacheKey); err != nil {
		uc.log.WithContext(ctx).Warnf("删除商品缓存失败: key=%s, err=%v", cacheKey, err)
		return err
	}
	uc.log.WithContext(ctx).Debugf("商品缓存已失效: productID=%d, activityID=%d", productID, activityID)
	return nil
}

// InvalidateActivityCache 主动失效活动缓存
func (uc *SeckillUsecase) InvalidateActivityCache(ctx context.Context) error {
	if err := uc.Cache.Del(ctx, cacheKeyActivity); err != nil {
		uc.log.WithContext(ctx).Warnf("删除活动缓存失败: %v", err)
		return err
	}
	uc.log.WithContext(ctx).Debug("活动缓存已失效")
	return nil
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

// cancelOrder 内部取消订单
func (uc *SeckillUsecase) cancelOrder(ctx context.Context, orderNo string, reason string) error {
	return uc.tx.ExecTx(ctx, func(txCtx context.Context) error {
		// 1.获取订单信息（带行锁）
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

		// 4.恢复 MySQL 库存
		if err := uc.Repo.RestoreStock(txCtx, order.SkuID, uint32(order.Quantity)); err != nil {
			uc.log.WithContext(txCtx).Errorf("恢复MySQL库存失败: orderNo=%s, err=%v", orderNo, err)
		}

		// 5.恢复 Redis 库存
		if err := uc.Cache.RollbackStock(txCtx, order.SkuID, int(order.Quantity)); err != nil {
			uc.log.WithContext(txCtx).Errorf("恢复Redis库存失败: orderNo=%s, err=%v", orderNo, err)
		}

		// 6.删除用户购买标记
		if err := uc.Cache.RemoveUserBuy(txCtx, order.SkuID, order.UserID); err != nil {
			uc.log.WithContext(txCtx).Warnf("删除用户购买标记失败: %v", err)
		}

		// 7.恢复优惠券（如果使用优惠券）
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

// WarmUpSeckillCache 预热秒杀缓存
func (uc *SeckillUsecase) WarmUpSeckillCache(ctx context.Context, activityID uint64) error {
	uc.log.WithContext(ctx).Infof("开始预热缓存, activity=%d", activityID)

	// 获取活动商品列表
	products, _, err := uc.Repo.ListSeckillProducts(ctx, activityID, 1, 100, 0)
	if err != nil {
		uc.log.WithContext(ctx).Errorf("获取商品列表失败: %v", err)
		return err
	}

	// 转化为缓存
	cachedProducts := make([]*CachedSeckillProduct, 0, len(products))
	for _, p := range products {
		detail, err := uc.Repo.GetSeckillProductDetail(ctx, p.ProductID, activityID)
		if err != nil {
			uc.log.WithContext(ctx).Warnf("获取商品详情失败: productID=%d, err=%v", p.ProductID, err)
			continue
		}

		cachedProducts = append(cachedProducts, &CachedSeckillProduct{
			SkuID:          p.SkuID,
			ProductID:      p.ProductID,
			ActivityID:     activityID, // 添加活动ID
			Name:           detail.Name,
			MainImage:      detail.MainImage,
			SeckillPrice:   detail.SeckillPrice,
			MarketPrice:    detail.MarketPrice,
			AvailableStock: detail.AvailableStock,
			TotalStock:     detail.TotalStock,
			LimitNum:       detail.LimitNum,
			StartTime:      detail.StartTime,
			EndTime:        detail.EndTime,
			ActivityStatus: detail.ActivityStatus,
		})
	}
	// 使用批量接口预热（一次Pipeline完成）
	if err := uc.Cache.BatchSetProducts(ctx, cachedProducts); err != nil {
		uc.log.WithContext(ctx).Errorf("批量预热缓存失败: %v", err)
		return err
	}

	// 预热活动信息
	activity, _, err := uc.Repo.GetCurrentActivity(ctx)
	if err == nil && activity != nil {
		if err := uc.Cache.SetCurrentActivity(ctx, activity, 30*time.Second); err != nil {
			uc.log.WithContext(ctx).Warnf("预热活动信息失败: %v", err)
		}
	}

	uc.log.WithContext(ctx).Infof("预热缓存完成, 商品数量=%d", len(cachedProducts))
	return nil
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

// generateOrderNo 生成订单号
func generateOrderNo(userID uint64) string {
	// 格式：时间戳(纳秒) + 用户ID后3位 + 随机数
	return fmt.Sprintf("%d%d%d",
		time.Now().UnixNano(),
		userID%1000,
		time.Now().UnixNano()%10000)
}
