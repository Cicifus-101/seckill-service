package biz

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-kratos/kratos/v2/log"
	"seckill-service/internal/observability"

	"seckill-service/internal/mq"
	"time"
)

const (
	OrderStatusPending = 0 // 待支付
	OrderStatusPaid    = 1 // 已支付
	OrderStatusCancel  = 2 // 已取消
)

const (
	OrderTimeoutMinutes = 15                       // 订单超时时间（分钟）
	OrderTimeoutSeconds = OrderTimeoutMinutes * 60 // 订单超时秒数
)

const (
	PayStatusCreated                 = "CREATED"
	PayStatusSuccess                 = "SUCCESS"
	PayStatusFailed                  = "FAILED"
	PayStatusSuccessButOrderCanceled = "SUCCESS_BUT_ORDER_CANCELED" // 支付成功但是订单已经取消
)

const (
	// 缓存Key前缀
	cacheKeyActivity = "seckill:activity:current" // 当前活动
)

const mockPaySecret = "seckill_mock_pay_secret"

// Transaction 事务接口
type Transaction interface {
	ExecTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// SkuStock SKU库存信息
type SkuStock struct {
	ID           uint64
	Stock        int32
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
	Canceler   *OrderCancelService
	IDGen      IDGenerator
	log        *log.Helper
	tx         Transaction
}

func NewSeckillUsecase(repo SeckillRepo, cache CacheRepo, mq MQProducer,
	limiter RateLimiter, idempotent IdempotentChecker, delayQueue DelayQueue, canceler *OrderCancelService,
	idGen IDGenerator, logger log.Logger, tx Transaction) *SeckillUsecase {
	return &SeckillUsecase{
		Repo:       repo,
		Cache:      cache,
		MQ:         mq,
		Limiter:    limiter,
		Idempotent: idempotent,
		DelayQueue: delayQueue,
		Canceler:   canceler,
		IDGen:      idGen,
		log:        log.NewHelper(log.With(logger, "module", "usecase/seckill")),
		tx:         tx,
	}
}

// ListSeckillProducts 获取秒杀商品列表
func (uc *SeckillUsecase) ListSeckillProducts(ctx context.Context, userID, activityID int64, page, pageSize, sortType int32) (_ *SeckillProductsResult, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.ListSeckillProducts")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "ListSeckillProducts", result, start)
	}()

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
			record, err := uc.Repo.CheckUserBuyRecord(ctx, uint64(userID), uint64(activityID))
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
		hasBought, _ := uc.Cache.CheckUserBuy(ctx, activityID, detail.SkuID, userID)
		if hasBought {
			res.UserStatus = &UserSeckillStatus{
				HasBought: true,
				CanBuy:    false,
				Message:   "您已参与过该秒杀活动",
			}
		} else {
			// 查数据库确认
			record, err := uc.Repo.CheckUserBuyRecord(ctx, userID, activityID)
			if err == nil {
				res.UserStatus = uc.buildUserStatus(detail, record)
			}
		}
	}
	return res, nil
}

// getProductDetailWithMutex 带互斥锁的缓存获取（布隆防缓存击穿）
func (uc *SeckillUsecase) getProductDetailWithMutex(ctx context.Context, productID, activityID uint64) (*SeckillProductDetail, error) {

	// 1. 先查 Bloom，过滤明显不存在的商品
	bloomOK, err := uc.Cache.BloomExists(ctx, activityID, productID)
	if err == nil && !bloomOK {
		return nil, ErrProductNotFound
	}

	// 2. 先查缓存
	detail, err := uc.Cache.GetProductDetail(ctx, productID, activityID)
	if err == nil && detail != nil {
		return detail, nil
	}

	// 3. 分布式锁防击穿
	lockKey := fmt.Sprintf("lock:product:%d:%d", productID, activityID)
	locked, err := uc.Cache.SetNX(ctx, lockKey, "1", 5*time.Second)
	if err != nil {
		detail, cacheErr := uc.Cache.GetProductDetail(ctx, productID, activityID)
		if cacheErr == nil && detail != nil {
			return detail, nil
		}

		uc.log.WithContext(ctx).Warnf("获取商品锁失败: %v", err)
		return nil, ErrSystemBusy
	}

	if locked {
		defer uc.Cache.Del(ctx, lockKey)

		// double check
		detail, err = uc.Cache.GetProductDetail(ctx, productID, activityID)
		if err == nil && detail != nil {
			return detail, nil
		}

		// 查 DB
		detail, err = uc.Repo.GetSeckillProductDetail(ctx, productID, activityID)
		if err != nil {
			if errors.Is(err, ErrProductNotFound) {
				return nil, ErrProductNotFound
			}
			return nil, err
		}

		// 回填缓存
		if err := uc.Cache.SetProductDetail(ctx, productID, activityID, detail, 10*time.Minute); err != nil {
			uc.log.WithContext(ctx).Warnf("设置商品详情缓存失败: %v", err)
		}
		return detail, nil
	}

	time.Sleep(50 * time.Millisecond)
	return uc.getProductDetailWithMutex(ctx, productID, activityID)
}

// CreateSeckillOrder 创建秒杀订单
func (uc *SeckillUsecase) CreateSeckillOrder(ctx context.Context, req *CreateOrderRequest) (_ *CreateOrderResult, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.CreateSeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "CreateSeckillOrder", result, start)
	}()

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

	// 4.2 限购检查
	if req.Quantity > productInfo.LimitNum {
		return nil, ErrExceedLimit
	}
	// 4.3 一人一单
	hasBought, err := uc.Cache.CheckUserBuy(ctx, req.ActivityID, req.SkuID, req.UserID)
	if err == nil && hasBought {
		uc.log.WithContext(ctx).Warnf("用户已购买(Redis), user=%d, sku=%d", req.UserID, req.SkuID)
		return nil, ErrAlreadyBought
	}
	record, err := uc.Repo.CheckUserBuyRecord(ctx, req.UserID, req.ActivityID)
	if err != nil {
		return nil, err
	}
	if record.HasBought {
		uc.Cache.MarkUserBuy(ctx, req.ActivityID, req.SkuID, req.UserID, 3600)
		return nil, ErrAlreadyBought
	}

	orderAmount := productInfo.SeckillPrice * uint64(req.Quantity)
	couponDiscount := uint64(0)
	finalAmount := orderAmount
	if req.CouponID > 0 {
		finalAmount, couponDiscount, err = uc.applyCoupon(ctx, req.CouponID, req.UserID, orderAmount)
		if err != nil {
			return nil, err
		}
	}

	// 5.1 原子扣减redis库存
	result, err := uc.Cache.DeductStock(ctx, req.ActivityID, req.SkuID, req.UserID, int(req.Quantity))
	if err != nil {
		uc.log.WithContext(ctx).Errorf("Redis扣库存失败: %v", err)
		return nil, err
	}
	switch result {
	case -1:
		uc.log.WithContext(ctx).Warnf("Redis检测到重复购买, user=%d, sku=%d", req.UserID, req.SkuID)
		if req.CouponID > 0 {
			_ = uc.Repo.RestoreUserCoupon(ctx, req.CouponID)
			_ = uc.Cache.DeleteCoupon(ctx, req.CouponID)
		}
		return nil, ErrAlreadyBought
	case 0:
		uc.log.WithContext(ctx).Warnf("Redis库存不足或商品不存在, sku=%d", req.SkuID)
		if req.CouponID > 0 {
			_ = uc.Repo.RestoreUserCoupon(ctx, req.CouponID)
			_ = uc.Cache.DeleteCoupon(ctx, req.CouponID)
		}
		return nil, ErrInsufficientStock
	case 1:
	default:
		uc.log.WithContext(ctx).Errorf("Redis扣库存返回未知结果: %d", result)
		if req.CouponID > 0 {
			_ = uc.Repo.RestoreUserCoupon(ctx, req.CouponID)
			_ = uc.Cache.DeleteCoupon(ctx, req.CouponID)
		}
		return nil, fmt.Errorf("扣库存失败")
	}

	// 6.1 构造MQ消息（异步下单）

	msg := &mq.SeckillOrderMessage{
		OrderNo:        uc.IDGen.NextString(),
		RequestID:      req.RequestID,
		UserID:         req.UserID,
		SkuID:          req.SkuID,
		ActivityID:     req.ActivityID,
		ProductID:      req.ProductID,
		ProductName:    productInfo.Name,
		ProductImage:   productInfo.MainImage,
		Quantity:       int(req.Quantity),
		AddressID:      req.AddressID,
		CouponID:       req.CouponID,
		OrderAmount:    orderAmount,
		CouponDiscount: couponDiscount,
		FinalAmount:    finalAmount,
		SeckillPrice:   productInfo.SeckillPrice,
		Version:        productInfo.Version,
		Timestamp:      time.Now().Unix(),
		EventID:        uc.IDGen.NextString(),
		TraceID:        observability.TraceID(ctx),
		RetryCount:     0,
	}

	_ = uc.Cache.SetPendingReservation(ctx, &PendingReservation{
		OrderNo:    msg.OrderNo,
		RequestID:  msg.RequestID,
		UserID:     msg.UserID,
		ActivityID: msg.ActivityID,
		SkuID:      msg.SkuID,
		Quantity:   msg.Quantity,
		CreatedAt:  time.Now().Unix(),
	}, 20*time.Minute)

	// 6.2 发送 MQ 消息
	if err := uc.MQ.Send(ctx, msg); err != nil {
		_ = uc.Cache.DeletePendingReservation(ctx, msg.RequestID)

		// 发送失败，回滚 Redis 库存
		uc.log.WithContext(ctx).Errorf("发送MQ消息失败: %v", err)
		_ = uc.Cache.RollbackStock(ctx, req.ActivityID, req.SkuID, int(req.Quantity))
		_ = uc.Cache.RemoveUserBuy(ctx, req.ActivityID, req.SkuID, req.UserID)

		if req.CouponID > 0 {
			_ = uc.Repo.RestoreUserCoupon(ctx, req.CouponID)
			_ = uc.Cache.DeleteCoupon(ctx, req.CouponID)
		}

		return nil, err
	}

	uc.log.WithContext(ctx).Infof("订单创建请求已接收, orderNo=%s, user=%d", msg.OrderNo, req.UserID)
	_ = uc.Cache.MarkUserBuy(ctx, req.ActivityID, req.SkuID, req.UserID, 3600)

	return &CreateOrderResult{
		OrderNo:          msg.OrderNo,
		OrderAmount:      orderAmount,
		CouponDiscount:   couponDiscount,
		FinalAmount:      finalAmount,
		Status:           OrderStatusPending,
		SeckillPrice:     productInfo.SeckillPrice,
		Quantity:         req.Quantity,
		RemainingSeconds: OrderTimeoutSeconds,
		Message:          "排队中，请稍后查询结果",
	}, nil
}

// RollbackSeckillReservation 消费处理失败的回滚
func (uc *SeckillUsecase) RollbackSeckillReservation(ctx context.Context, msg *mq.SeckillOrderMessage) error {
	if msg == nil {
		return errors.New("order message is nil")
	}

	if err := uc.Cache.RollbackStock(ctx, msg.ActivityID, msg.SkuID, msg.Quantity); err != nil {
		return err
	}

	if err := uc.Cache.RemoveUserBuy(ctx, msg.ActivityID, msg.SkuID, msg.UserID); err != nil {
		return err
	}

	if msg.CouponID > 0 {
		_ = uc.Repo.RestoreUserCoupon(ctx, msg.CouponID)
		_ = uc.Cache.DeleteCoupon(ctx, msg.CouponID)
	}

	_ = uc.Cache.DeletePendingReservation(ctx, msg.RequestID)
	return nil
}

func (uc *SeckillUsecase) ReplayDeadLetter(ctx context.Context, eventID string) error {
	dlq, err := uc.Repo.GetDeadLetterMessageByEventID(ctx, eventID)
	if err != nil {
		return err
	}

	var msg mq.SeckillOrderMessage
	if err := json.Unmarshal([]byte(dlq.RawPayload), &msg); err != nil {
		return err
	}

	// 恢复 trace / retry 信息
	if msg.TraceID == "" {
		msg.TraceID = dlq.TraceID
	}

	msg.RetryCount = dlq.RetryCount + 1
	if msg.EventID == "" {
		msg.EventID = dlq.EventID
	}

	// 重新投递到retry topic
	if err := uc.MQ.SendToRetry(ctx, &msg, msg.RetryCount, 10*time.Second); err != nil {
		return err
	}

	return uc.Repo.MarkDeadLetterReplayed(ctx, eventID)
}

// 事务落库：消费者消费成功后再提交offset的基础
func (uc *SeckillUsecase) ConfirmSeckillOrder(ctx context.Context, msg *mq.SeckillOrderMessage) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.ConfirmSeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "ConfirmSeckillOrder", result, start)
	}()

	if msg == nil {
		return errors.New("order message is null")
	}
	err = uc.tx.ExecTx(ctx, func(txCtx context.Context) error {
		address, err := uc.Repo.GetUserAddress(txCtx, msg.AddressID)
		if err != nil {
			return err
		}

		orderAmount := msg.OrderAmount
		if orderAmount == 0 {
			orderAmount = msg.SeckillPrice * uint64(msg.Quantity)
		}

		finalAmount := msg.FinalAmount
		if finalAmount == 0 {
			if msg.CouponDiscount > orderAmount {
				finalAmount = 0
			} else {
				finalAmount = orderAmount - msg.CouponDiscount
			}
		}

		orderNo, err := uc.Repo.CreateOrder(txCtx, &Order{
			OrderNo:        msg.OrderNo,
			UserID:         msg.UserID,
			RequestID:      msg.RequestID,
			ActivityID:     msg.ActivityID,
			ProductID:      msg.ProductID,
			SkuID:          msg.SkuID,
			ProductName:    msg.ProductName,
			ProductImage:   msg.ProductImage,
			SeckillPrice:   msg.SeckillPrice,
			Quantity:       int64(msg.Quantity),
			OrderAmount:    orderAmount,
			CouponID:       msg.CouponID,
			CouponDiscount: msg.CouponDiscount,
			FinalAmount:    finalAmount,
			AddressID:      msg.AddressID,
			Status:         OrderStatusPending,
		})
		if err != nil {
			return err
		}
		if orderNo != msg.OrderNo {
			return ErrOrderExists
		}
		if err := uc.Repo.CreateOrderShipping(txCtx, orderNo, address); err != nil {
			return err
		}

		if err := uc.Repo.DecreaseStock(txCtx, msg.SkuID, uint32(msg.Quantity), msg.Version); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// pending 是redis已预扣，但 MySQL 还未确认成功。只有订单事务成功，才能删除
	_ = uc.Cache.DeletePendingReservation(ctx, msg.RequestID)

	// 在这里进行订单任务的添加
	if err := uc.DelayQueue.Add(ctx, msg.OrderNo, OrderTimeoutMinutes*time.Minute); err != nil {
		uc.log.WithContext(ctx).Warnf("添加延迟取消任务失败，但订单已落库: orderNo=%s err=%v", msg.OrderNo, err)
	}
	return nil
}

func (uc *SeckillUsecase) getActivityFromCacheWithMutex(ctx context.Context) (*Activity, error) {
	cacheKey := cacheKeyActivity // 缓存空值

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
func (uc *SeckillUsecase) GetSeckillOrder(ctx context.Context, orderNo string, userID uint64) (_ *OrderInfo, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.GetSeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "GetSeckillOrder", result, start)
	}()

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
func (uc *SeckillUsecase) PaySeckillOrder(ctx context.Context, req *PayOrderRequest) (_ *PayOrderResult, err error) {
	var result *PayOrderResult

	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.PaySeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "PaySeckillOrder", result, start)
	}()

	err = uc.tx.ExecTx(ctx, func(txctx context.Context) error {
		// 1.获取订单信息(行锁)
		order, err := uc.Repo.GetOrderForUpdate(txctx, req.OrderNo)
		if err != nil {
			return err
		}

		// 检查归属
		if order.UserID != req.UserID {
			return ErrUserNotMatch
		}

		// 2.检查订单状态（防止重复支付、以及对已处理订单进行操作）
		if order.Status != OrderStatusPending {
			return ErrOrderStatusIncorrect
		}

		// 3. 超时校验
		if uc.isOrderTimeout(order.CreateTime) {
			return ErrOrderTimeout
		}

		platformNumber := "P" + uc.IDGen.NextString()
		// 5.创建支付记录
		payInfo := &PayInfo{
			OrderNo:        req.OrderNo,
			UserID:         req.UserID,
			PayPlatform:    req.PayPlatform,
			PlatformNumber: platformNumber,
			PlatformStatus: PayStatusCreated,
			PayAmount:      order.FinalAmount,
			PayTime:        nil, // 真正支付成功回调时间
		}

		// 这里解决的是分布式，防止在事务A还没创建的时候事务B尝试创建，通过唯一键触发检查
		if err := uc.Repo.CreatePayInfo(txctx, payInfo); err != nil {
			if errors.Is(err, ErrPaymentExists) {
				existPay, getErr := uc.Repo.GetPayInfoByOrderNo(txctx, req.OrderNo)
				if getErr != nil {
					return getErr
				}
				result = &PayOrderResult{
					Success:        true,
					PayAmount:      order.FinalAmount,
					PlatformNumber: existPay.PlatformNumber,
				}
				return nil
			}
			return fmt.Errorf("创建支付单失败: %w", err)
		}

		result = &PayOrderResult{
			Success:        true,
			PayAmount:      order.FinalAmount,
			PlatformNumber: platformNumber,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}

// 支付回调处理方法
func (uc *SeckillUsecase) HandlePayCallback(ctx context.Context, req *PayCallbackRequest) (_ *PayCallbackResult, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.HandlePayCallback")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "HandlePayCallback", result, start)
	}()

	if err := uc.verifyPayCallbackSign(req); err != nil {
		uc.log.WithContext(ctx).Warnf("支付回调验签失败: orderNo=%s platformNumber=%s err=%v", req.OrderNo, req.PlatformNumber, err)
		return &PayCallbackResult{Success: false, Message: "invalid sign"}, err
	}

	var callbackResult *PayCallbackResult

	err = uc.tx.ExecTx(ctx, func(txCtx context.Context) error {
		payInfo, err := uc.Repo.GetPayInfoByPlatformNumber(txCtx, req.PlatformNumber)
		if err != nil {
			return err
		}

		if payInfo.OrderNo != req.OrderNo {
			return fmt.Errorf("支付流水订单不匹配")
		}

		if payInfo.PayAmount != req.PayAmount {
			uc.log.WithContext(txCtx).Warnf("支付金额不一致: orderNo=%s expected=%d actual=%d",
				req.OrderNo, payInfo.PayAmount, req.PayAmount)
			return ErrPayAmountMismatch
		}

		// 重复成功回调，幂等返回成功
		if payInfo.PlatformStatus == PayStatusSuccess ||
			payInfo.PlatformStatus == PayStatusSuccessButOrderCanceled {
			callbackResult = &PayCallbackResult{
				Success: true,
				Message: "duplicate callback ignored",
			}
			return nil
		}

		order, err := uc.Repo.GetOrderForUpdate(txCtx, req.OrderNo)
		if err != nil {
			return err
		}

		payTime := time.Unix(req.PayTime, 0) //时间戳转换成time.Time

		if req.PlatformStatus != PayStatusSuccess {
			if err := uc.Repo.UpdatePayInfoStatus(txCtx, req.PlatformNumber, PayStatusFailed, &payTime); err != nil {
				return err
			}
			callbackResult = &PayCallbackResult{
				Success: true,
				Message: "payment failed recorded",
			}
			return nil
		}

		if order.Status == OrderStatusPaid {
			if err := uc.Repo.UpdatePayInfoStatus(txCtx, req.PlatformNumber, PayStatusSuccess, &payTime); err != nil {
				return err
			}

			callbackResult = &PayCallbackResult{
				Success: true,
				Message: "order already paid",
			}
			return nil
		}

		if order.Status == OrderStatusCancel {
			if err := uc.Repo.UpdatePayInfoStatus(txCtx, req.PlatformNumber, PayStatusSuccessButOrderCanceled, &payTime); err != nil {
				return err
			}
			callbackResult = &PayCallbackResult{
				Success: true,
				Message: "order canceled, payment recorded for refund",
			}
			return nil
		}

		if order.Status != OrderStatusPending {
			return ErrOrderStatusIncorrect
		}

		if err := uc.Repo.UpdateOrderStatus(txCtx, req.OrderNo, OrderStatusPending, OrderStatusPaid); err != nil {
			return err
		}

		if err := uc.Repo.UpdatePayInfoStatus(txCtx, req.PlatformNumber, PayStatusSuccess, &payTime); err != nil {
			return err
		}

		callbackResult = &PayCallbackResult{
			Success: true,
			Message: "payment success",
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return callbackResult, nil
}

func (uc *SeckillUsecase) verifyPayCallbackSign(req *PayCallbackRequest) error {
	expected := uc.buildPayCallbackSign(req)
	if expected != req.Sign {
		return errors.New("invalid payment callback sign")
	}
	return nil
}
func (uc *SeckillUsecase) buildPayCallbackSign(req *PayCallbackRequest) string {
	raw := fmt.Sprintf("%s|%s|%d|%s|%d|%s",
		req.OrderNo,
		req.PlatformNumber,
		req.PayAmount,
		req.PlatformStatus,
		req.PayTime,
		mockPaySecret,
	)

	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
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
func (uc *SeckillUsecase) CancelOrder(ctx context.Context, orderNo string, userID uint64, reason string) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.CancelOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "CancelOrder", result, start)
	}()

	// 获取订单信息验证权限
	order, err := uc.Repo.GetOrder(ctx, orderNo)
	if err != nil {
		return err
	}
	if order.UserID != userID {
		return ErrUserNotMatch
	}

	return uc.Canceler.CancelTimeoutOrder(ctx, orderNo, reason)
}

// cancelOrder 内部取消订单
func (uc *SeckillUsecase) cancelOrder(ctx context.Context, orderNo string, reason string) error {
	return uc.Canceler.CancelTimeoutOrder(ctx, orderNo, reason)
}

// GetSeckillResult 获取秒杀结果（用于轮询）
func (uc *SeckillUsecase) GetSeckillResult(ctx context.Context, userID uint64, requestID string) (*SeckillResult, error) {
	if requestID == "" {
		return nil, errors.New("request_id 不能为空")
	}
	order, err := uc.Repo.GetOrderByRequestID(ctx, requestID)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			return &SeckillResult{
				Status:  0,
				Message: "处理中",
			}, nil
		}
		return nil, err
	}
	if order.UserID != userID {
		return nil, ErrUserNotMatch
	}
	switch order.Status {
	case OrderStatusPending:
		return &SeckillResult{
			Status:      1,
			OrderNo:     order.OrderNo,
			OrderAmount: order.OrderAmount,
			Message:     "下单成功，待支付",
		}, nil

	case OrderStatusPaid:
		return &SeckillResult{
			Status:      1,
			OrderNo:     order.OrderNo,
			OrderAmount: order.FinalAmount,
			Message:     "支付成功",
		}, nil

	case OrderStatusCancel:
		return &SeckillResult{
			Status:  2,
			OrderNo: order.OrderNo,
			Message: "订单已取消",
		}, nil

	default:
		return &SeckillResult{
			Status:  2,
			OrderNo: order.OrderNo,
			Message: "订单状态异常",
		}, nil
	}
}

// WarmUpSeckillCache 预热秒杀缓存
func (uc *SeckillUsecase) WarmUpSeckillCache(ctx context.Context, activityID uint64) (err error) {
	uc.log.WithContext(ctx).Infof("开始预热缓存, activity=%d", activityID)

	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.WarmUpSeckillCache")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "WarmUpSeckillCache", result, start)
	}()

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
func (uc *SeckillUsecase) applyCoupon(ctx context.Context, couponID, userID uint64, orderAmount uint64) (finalAmount, discount uint64, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "biz.applyCoupon")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("biz", "applyCoupon", result, start)
	}()

	if couponID == 0 {
		return orderAmount, 0, nil
	}

	coupon, err := uc.Repo.GetUserCouponForUse(ctx, couponID, userID)
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
	if err := uc.Repo.UseUserCoupon(ctx, coupon.ID); err != nil {
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
