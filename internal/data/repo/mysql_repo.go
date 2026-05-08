package repo

import (
	"context"
	"errors"
	"seckill-service/internal/data"
	"seckill-service/internal/observability"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"seckill-service/internal/biz"
	coreModel "seckill-service/internal/data/core/model"
	payModel "seckill-service/internal/data/pay/model"
	productModel "seckill-service/internal/data/product/model"
)

const (
	delayQueueKey = "delay:orders"
	maxRetry      = 3
)

type mysqlRepo struct {
	data *data.Data
	log  *log.Helper
}

// NewSeckillRepo 创建秒杀仓储
func NewMysqlRepo(d *data.Data, logger log.Logger) biz.SeckillRepo {
	return &mysqlRepo{
		data: d,
		log:  log.NewHelper(log.With(logger, "module", "repo/seckill")),
	}
}

// ListSeckillProducts 获取秒杀商品列表
func (r *mysqlRepo) ListSeckillProducts(ctx context.Context, activityID uint64, page, pageSize int32, sortType int32) ([]*biz.SeckillProduct, int64, error) {
	q := r.data.GetCoreQuery(ctx)
	productQ := r.data.GetProductQuery(ctx)

	// 先查询核心库的秒杀 SKU
	skuQuery := q.SeckillSku.WithContext(ctx)
	if activityID > 0 {
		skuQuery = skuQuery.Where(q.SeckillSku.ActivityID.Eq(activityID))
	}

	// 查询总数
	total, err := skuQuery.Count()
	if err != nil {
		return nil, 0, err
	}

	// 排序
	switch sortType {
	case 2:
		skuQuery = skuQuery.Order(q.SeckillSku.SeckillPrice)
	case 3:
		skuQuery = skuQuery.Order(q.SeckillSku.SeckillPrice.Desc())
	case 4:
		skuQuery = skuQuery.Order(q.SeckillSku.AvailableStock)
	case 5:
		skuQuery = skuQuery.Order(q.SeckillSku.AvailableStock.Desc())
	default:
		skuQuery = skuQuery.Order(q.SeckillSku.ID)
	}

	//分页
	offset := (page - 1) * pageSize
	var skus []coreModel.SeckillSku
	err = skuQuery.Offset(int(offset)).Limit(int(pageSize)).Scan(&skus)
	if err != nil {
		return nil, 0, err
	}

	// 获取所有sku对应的ProductID
	productIDs := make([]uint64, 0, len(skus))
	for _, sku := range skus {
		productIDs = append(productIDs, sku.ProductID)
	}

	// 批量查询商品信息
	productsMap := make(map[uint64]*productModel.Product)
	if len(productIDs) > 0 {
		var products []*productModel.Product
		products, err := productQ.Product.WithContext(ctx).
			Where(productQ.Product.ID.In(productIDs...)).
			Find()
		if err != nil {
			return nil, 0, err
		}
		for _, p := range products {
			productsMap[p.ID] = p
		}
	}

	// 合并数据
	result := make([]*biz.SeckillProduct, 0, len(skus))
	for _, s := range skus {
		prod := productsMap[s.ProductID]
		saleRate := int64(0)
		if s.TotalStock > 0 {
			saleRate = int64((s.TotalStock - s.AvailableStock) * 100 / s.TotalStock)
		}
		result = append(result, &biz.SeckillProduct{
			SkuID:          s.ID,
			ProductID:      s.ProductID,
			ActivityID:     s.ActivityID,
			Name:           "",
			MainImage:      "",
			SeckillPrice:   s.SeckillPrice,
			MarketPrice:    s.MarketPrice,
			AvailableStock: int64(s.AvailableStock),
			TotalStock:     int64(s.TotalStock),
			LimitNum: func() int64 {
				if s.LimitNum != nil {
					return int64(*s.LimitNum)
				}
				return 1
			}(),
			SaleRate: saleRate,
		})
		if prod != nil {
			result[len(result)-1].Name = prod.Name
			result[len(result)-1].MainImage = prod.MainImage
		}
	}
	return result, total, nil
}

// GetSeckillProductDetail 获取秒杀商品详情
func (r *mysqlRepo) GetSeckillProductDetail(ctx context.Context, productID, activityID uint64) (*biz.SeckillProductDetail, error) {
	q := r.data.GetCoreQuery(ctx)
	productQ := r.data.GetProductQuery(ctx)

	// 查询秒杀sku+活动信息
	sku, err := q.SeckillSku.WithContext(ctx).
		Where(q.SeckillSku.ProductID.Eq(productID), q.SeckillSku.ActivityID.Eq(activityID)).
		First()
	if err != nil {
		return nil, err
	}

	activity, err := q.SeckillActivity.WithContext(ctx).
		Where(q.SeckillActivity.ID.Eq(sku.ActivityID)).
		First()
	if err != nil {
		return nil, err
	}

	// 查询商品信息
	product, err := productQ.Product.WithContext(ctx).
		Where(productQ.Product.ID.Eq(productID)).
		First()
	if err != nil {
		return nil, err
	}

	// 计算倒计时
	now := time.Now()
	remainingSeconds := int64(0)
	if now.Before(activity.StartTime) {
		remainingSeconds = int64(activity.StartTime.Sub(now).Seconds())
	} else if now.Before(activity.EndTime) {
		remainingSeconds = int64(activity.EndTime.Sub(now).Seconds())
	}

	limitNum := int64(1)
	if sku.LimitNum != nil {
		limitNum = int64(*sku.LimitNum)
	}

	detail := ""
	if product.Detail != nil {
		detail = *product.Detail
	}

	return &biz.SeckillProductDetail{
		ProductID:        productID,
		Name:             product.Name,
		Subtitle:         product.Subtitle,
		MainImage:        product.MainImage,
		Detail:           detail,
		SkuID:            sku.ID,
		SeckillPrice:     sku.SeckillPrice,
		MarketPrice:      sku.MarketPrice,
		TotalStock:       int64(sku.TotalStock),
		AvailableStock:   int64(sku.AvailableStock),
		LimitNum:         limitNum,
		ActivityID:       activity.ID,
		ActivityTitle:    activity.Title,
		Description:      activity.Description,
		StartTime:        activity.StartTime.Format("2006-01-02 15:04:05"),
		EndTime:          activity.EndTime.Format("2006-01-02 15:04:05"),
		ActivityStatus:   int64(activity.Status),
		RemainingSeconds: remainingSeconds,
		Version:          sku.Version,
	}, nil
}

// GetCurrentActivity 获取当前活动
func (r *mysqlRepo) GetCurrentActivity(ctx context.Context) (*biz.Activity, int64, error) {
	q := r.data.GetCoreQuery(ctx)
	now := time.Now()

	activity, err := q.SeckillActivity.WithContext(ctx).
		Where(
			q.SeckillActivity.StartTime.Lte(now),
			q.SeckillActivity.EndTime.Gte(now),
			q.SeckillActivity.Status.Eq(1),
		).
		Order(q.SeckillActivity.ID.Desc()).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, biz.ErrNoActiveActivity
		}
		return nil, 0, err
	}

	// 查询活动商品数量
	count, err := q.SeckillSku.WithContext(ctx).
		Where(q.SeckillSku.ActivityID.Eq(activity.ID)).
		Count()

	if err != nil {
		return nil, 0, err
	}

	remainingSeconds := int64(activity.EndTime.Sub(now).Seconds())

	return &biz.Activity{
		ID:               activity.ID,
		Title:            activity.Title,
		Description:      activity.Description,
		StartTime:        activity.StartTime.Format("2006-01-02 15:04:05"),
		EndTime:          activity.EndTime.Format("2006-01-02 15:04:05"),
		Status:           activity.Status,
		RemainingSeconds: remainingSeconds,
	}, count, nil
}

// CreatePayInfo 创建支付记录
func (r *mysqlRepo) CreatePayInfo(ctx context.Context, payInfo *biz.PayInfo) error {
	q := r.data.GetPayQuery(ctx)

	modelPayInfo := &payModel.PayInfo{
		OrderNo:        payInfo.OrderNo,
		UserID:         payInfo.UserID,
		PayPlatform:    payInfo.PayPlatform,
		PlatformNumber: payInfo.PlatformNumber,
		PlatformStatus: payInfo.PlatformStatus,
		PayAmount:      payInfo.PayAmount,
	}

	if payInfo.PayTime != nil {
		modelPayInfo.PayTime = payInfo.PayTime
	}

	err := q.PayInfo.WithContext(ctx).Create(modelPayInfo)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) ||
			(err.Error() != "" && strings.Contains(err.Error(), "Duplicate entry")) {
			return biz.ErrPaymentExists
		}
		return err
	}

	return nil
}

// LockStock 锁定库存（使用SELECT FOR UPDATE）
func (r *mysqlRepo) LockStock(ctx context.Context, skuID uint64, quantity uint32) (*biz.SkuStock, error) {
	q := r.data.GetCoreQuery(ctx)

	// 使用 FOR UPDATE 悲观锁
	sku, err := q.SeckillSku.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(q.SeckillSku.ID.Eq(skuID),
			q.SeckillSku.AvailableStock.Gte(quantity)).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrInsufficientStock
		}
		return nil, err
	}

	return &biz.SkuStock{ // 这里使用悲观锁的同时使用乐观锁（没必要）
		ID:           sku.ID,
		SeckillPrice: sku.SeckillPrice,
		Version:      sku.Version,
	}, nil
}

// DecreaseStock 扣减库存（乐观锁）
func (r *mysqlRepo) DecreaseStock(ctx context.Context, skuID uint64, quantity uint32, version uint32) error {
	q := r.data.GetCoreQuery(ctx)

	info, err := q.SeckillSku.WithContext(ctx).
		Where(
			q.SeckillSku.ID.Eq(skuID),
			q.SeckillSku.AvailableStock.Gte(quantity),
		).
		UpdateSimple(
			q.SeckillSku.AvailableStock.Sub(quantity),
			q.SeckillSku.Version.Add(1),
		)

	if err != nil {
		return err
	}

	if info.RowsAffected == 0 {
		return biz.ErrInsufficientStock
	}

	return nil
}

// RestoreStock 恢复库存
func (r *mysqlRepo) RestoreStock(ctx context.Context, skuID uint64, quantity uint32) error {
	q := r.data.GetCoreQuery(ctx)

	info, err := q.SeckillSku.WithContext(ctx).
		Where(q.SeckillSku.ID.Eq(skuID)).
		UpdateSimple(
			q.SeckillSku.AvailableStock.Add(quantity),
			q.SeckillSku.Version.Add(1),
		)

	if err != nil {
		return err
	}
	if info.RowsAffected == 0 {
		return errors.New("恢复库存失败")
	}
	return nil
}

// CheckUserBuyRecord 检查用户购买记录
func (r *mysqlRepo) CheckUserBuyRecord(ctx context.Context, userID, activityID uint64) (*biz.UserBuyRecord, error) {
	q := r.data.GetCoreQuery(ctx)

	order, err := q.SeckillOrder.WithContext(ctx).
		Where(
			q.SeckillOrder.UserID.Eq(userID),
			q.SeckillOrder.ActivityID.Eq(activityID),
			q.SeckillOrder.Status.In(0, 1),
		).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &biz.UserBuyRecord{
				HasBought: false,
				Quantity:  0,
			}, nil
		}
		return nil, err
	}

	quantity := int64(0)
	if order.Quantity != nil {
		quantity = int64(*order.Quantity)
	}

	return &biz.UserBuyRecord{
		HasBought: true,
		Quantity:  quantity,
	}, nil
}

// GetUserAddress 获取用户地址
func (r *mysqlRepo) GetUserAddress(ctx context.Context, addressID uint64) (*biz.Address, error) {
	q := r.data.GetUserQuery(ctx)

	address, err := q.UserAddress.WithContext(ctx).
		Where(q.UserAddress.ID.Eq(addressID), q.UserAddress.IsDelete.Eq(0)).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrAddressNotFound
		}
		return nil, err
	}

	return &biz.Address{
		ID:            address.ID,
		ReceiverName:  address.ReceiverName,
		ReceiverPhone: address.ReceiverPhone,
		Province:      address.Province,
		City:          address.City,
		District:      address.District,
		DetailAddress: address.DetailAddress,
		IsDefault:     address.IsDefault == 1,
	}, nil
}

// GetCoupon 获取优惠券（带行锁）
func (r *mysqlRepo) GetCoupon(ctx context.Context, couponID uint64) (*biz.Coupon, error) {
	q := r.data.GetCoreQuery(ctx)
	now := time.Now()

	coupon, err := q.Coupon.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			q.Coupon.ID.Eq(couponID),
			q.Coupon.RemainCount.Gt(0),
			q.Coupon.StartTime.Lte(now),
			q.Coupon.EndTime.Gte(now),
		).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrCouponInvalid
		}
		return nil, err
	}

	return &biz.Coupon{
		ID:        coupon.ID,
		Name:      coupon.Name,
		Type:      coupon.Type,
		Value:     coupon.Value,
		MinAmount: coupon.MinAmount,
		Version:   coupon.Version,
	}, nil
}

// UseCoupon 使用优惠券
func (r *mysqlRepo) UseCoupon(ctx context.Context, couponID uint64, version uint32) error {
	q := r.data.GetCoreQuery(ctx)

	info, err := q.Coupon.WithContext(ctx).
		Where(
			q.Coupon.ID.Eq(couponID),
			q.Coupon.Version.Eq(version),
			q.Coupon.RemainCount.Gt(0),
		).
		UpdateColumns(map[string]interface{}{
			"remain_count": gorm.Expr("remain_count - 1"),
			"version":      gorm.Expr("version + 1"),
		})

	if err != nil {
		return err
	}

	if info.RowsAffected == 0 {
		return biz.ErrCouponUsed
	}

	return nil
}

// RestoreCoupon 恢复优惠券库存
func (r *mysqlRepo) RestoreCoupon(ctx context.Context, couponID uint64) error {
	q := r.data.GetCoreQuery(ctx)

	info, err := q.Coupon.WithContext(ctx).
		Where(q.Coupon.ID.Eq(couponID)).
		UpdateSimple(
			q.Coupon.RemainCount.Add(1),
			q.Coupon.Version.Add(1),
		)

	if err != nil {
		return err
	}
	if info.RowsAffected == 0 {
		return errors.New("恢复优惠券失败")
	}
	return nil
}

// CreateOrder 创建订单
func (r *mysqlRepo) CreateOrder(ctx context.Context, order *biz.Order) (orderNo string, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.CreateOrder")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "CreateOrder", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetCoreQuery(ctx)

	orderNo = order.OrderNo
	quantity := uint32(order.Quantity)

	modelOrder := &coreModel.SeckillOrder{
		OrderNo:        orderNo,
		RequestID:      order.RequestID,
		UserID:         order.UserID,
		ActivityID:     order.ActivityID,
		ProductID:      order.ProductID,
		SkuID:          order.SkuID,
		ProductName:    order.ProductName,
		ProductImage:   order.ProductImage,
		SeckillPrice:   order.SeckillPrice,
		Quantity:       &quantity,
		OrderAmount:    order.OrderAmount,
		CouponID:       nil,
		CouponDiscount: order.CouponDiscount,
		FinalAmount:    &order.FinalAmount,
		Status:         0,
	}

	if order.CouponID > 0 {
		couponID := order.CouponID
		modelOrder.CouponID = &couponID
	}
	if order.AddressID > 0 {
		addressID := order.AddressID
		modelOrder.AddressID = &addressID
	}

	err = q.SeckillOrder.WithContext(ctx).Create(modelOrder)
	if err != nil {
		// 检查唯一索引冲突
		if errors.Is(err, gorm.ErrDuplicatedKey) ||
			(err.Error() != "" && strings.Contains(err.Error(), "Duplicate entry")) {
			return "", biz.ErrOrderExists
		}
		return "", err
	}

	return orderNo, nil
}

// CreateOrderShipping 创建收货信息快照
func (r *mysqlRepo) CreateOrderShipping(ctx context.Context, orderNo string, address *biz.Address) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.CreateOrderShipping")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "CreateOrderShipping", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetCoreQuery(ctx)

	shipping := &coreModel.OrderShipping{
		OrderNo:       orderNo,
		ReceiverName:  address.ReceiverName,
		ReceiverPhone: address.ReceiverPhone,
		Province:      address.Province,
		City:          address.City,
		District:      address.District,
		DetailAddress: address.DetailAddress,
	}

	return q.OrderShipping.WithContext(ctx).Create(shipping)
}

// GetOrder 获取订单信息
func (r *mysqlRepo) GetOrder(ctx context.Context, orderNo string) (orderInfo *biz.OrderInfo, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.GetOrder")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "GetOrder", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetCoreQuery(ctx)

	order, err := q.SeckillOrder.WithContext(ctx).
		Where(q.SeckillOrder.OrderNo.Eq(orderNo)).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrOrderNotFound
		}
		return nil, err
	}

	// 查询收货信息
	shipping, _ := q.OrderShipping.WithContext(ctx).
		Where(q.OrderShipping.OrderNo.Eq(orderNo)).
		First()

	quantity := int64(0)
	if order.Quantity != nil {
		quantity = int64(*order.Quantity)
	}

	couponID := uint64(0)
	if order.CouponID != nil {
		couponID = *order.CouponID
	}

	finalAmount := order.OrderAmount
	if order.FinalAmount != nil {
		finalAmount = *order.FinalAmount
	}

	createTime := ""
	if order.CreateTime != nil {
		createTime = order.CreateTime.Format("2006-01-02 15:04:05")
	}

	orderInfo = &biz.OrderInfo{
		OrderNo:        order.OrderNo,
		UserID:         order.UserID,
		ActivityID:     order.ActivityID,
		ProductID:      order.ProductID,
		SkuID:          order.SkuID,
		ProductName:    order.ProductName,
		ProductImage:   order.ProductImage,
		SeckillPrice:   order.SeckillPrice,
		Quantity:       quantity,
		OrderAmount:    order.OrderAmount,
		CouponID:       couponID,
		CouponDiscount: order.CouponDiscount,
		FinalAmount:    finalAmount,
		Status:         int32(order.Status),
		CreateTime:     createTime,
	}

	// 支付倒计时
	if order.Status == 0 && order.CreateTime != nil {
		expireTime := order.CreateTime.Add(15 * time.Minute)
		remaining := int64(time.Until(expireTime).Seconds())
		if remaining > 0 {
			orderInfo.RemainingSeconds = remaining
		}
	}

	// 组装地址
	if shipping != nil {
		orderInfo.Address = &biz.AddressSnapshot{
			ReceiverName:  shipping.ReceiverName,
			ReceiverPhone: shipping.ReceiverPhone,
			Province:      shipping.Province,
			City:          shipping.City,
			District:      shipping.District,
			DetailAddress: shipping.DetailAddress,
		}
	}

	return orderInfo, nil
}

// GetOrderByRequestID 前端轮询查询订单结果
func (r *mysqlRepo) GetOrderByRequestID(ctx context.Context, requestID string) (*biz.OrderInfo, error) {
	q := r.data.GetCoreQuery(ctx)

	order, err := q.SeckillOrder.WithContext(ctx).
		Where(q.SeckillOrder.RequestID.Eq(requestID)).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrOrderNotFound
		}
		return nil, err
	}

	quantity := int64(0)
	if order.Quantity != nil {
		quantity = int64(*order.Quantity)
	}

	couponID := uint64(0)
	if order.CouponID != nil {
		couponID = *order.CouponID
	}

	createTime := ""
	if order.CreateTime != nil {
		createTime = order.CreateTime.Format("2006-01-02 15:04:05")
	}

	return &biz.OrderInfo{
		OrderNo:        order.OrderNo,
		UserID:         order.UserID,
		ActivityID:     order.ActivityID,
		ProductID:      order.ProductID,
		SkuID:          order.SkuID,
		ProductName:    order.ProductName,
		ProductImage:   order.ProductImage,
		SeckillPrice:   order.SeckillPrice,
		Quantity:       quantity,
		OrderAmount:    order.OrderAmount,
		CouponID:       couponID,
		CouponDiscount: order.CouponDiscount,
		FinalAmount: func() uint64 {
			if order.FinalAmount != nil {
				return *order.FinalAmount
			}
			return order.OrderAmount
		}(),
		Status:     int32(order.Status),
		CreateTime: createTime,
	}, nil
}

// GetOrderForUpdate 获取订单信息（带行锁）
func (r *mysqlRepo) GetOrderForUpdate(ctx context.Context, orderNo string) (orderInfo *biz.OrderInfo, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.GetOrderForUpdate")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "GetOrderForUpdate", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetCoreQuery(ctx)

	order, err := q.SeckillOrder.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(q.SeckillOrder.OrderNo.Eq(orderNo)).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrOrderNotFound
		}
		return nil, err
	}
	quantity := int64(0)
	if order.Quantity != nil {
		quantity = int64(*order.Quantity)
	}

	couponID := uint64(0)
	if order.CouponID != nil {
		couponID = *order.CouponID
	}

	finalAmount := order.OrderAmount
	if order.FinalAmount != nil {
		finalAmount = *order.FinalAmount
	}

	createTime := ""
	if order.CreateTime != nil {
		createTime = order.CreateTime.Format("2006-01-02 15:04:05")
	}

	return &biz.OrderInfo{
		OrderNo:      order.OrderNo,
		UserID:       order.UserID,
		ActivityID:   order.ActivityID,
		ProductID:    order.ProductID,
		SkuID:        order.SkuID,
		ProductName:  order.ProductName,
		ProductImage: order.ProductImage,
		SeckillPrice: order.SeckillPrice,
		Quantity:     quantity,
		OrderAmount:  order.OrderAmount,
		Status:       int32(order.Status),
		CreateTime:   createTime,
		CouponID:     couponID,
		FinalAmount:  finalAmount,
	}, nil
}

// UpdateOrderStatus 更新订单状态
func (r *mysqlRepo) UpdateOrderStatus(ctx context.Context, orderNo string, fromStatus int32, toStatus int32) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.UpdateOrderStatus")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "UpdateOrderStatus", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetCoreQuery(ctx)

	updates := map[string]interface{}{
		"status": toStatus,
	}

	if toStatus == biz.OrderStatusPaid {
		updates["pay_time"] = time.Now()
	}

	info, err := q.SeckillOrder.WithContext(ctx).
		Where(q.SeckillOrder.OrderNo.Eq(orderNo),
			q.SeckillOrder.Status.Eq(uint32(fromStatus))).
		Updates(updates)

	if err != nil {
		return err
	}

	if info.RowsAffected == 0 {
		return biz.ErrOrderStatusIncorrect
	}

	return nil
}

// GetPendingOrders 获取超时待支付订单
func (r *mysqlRepo) GetPendingOrders(ctx context.Context, timeoutMinutes int) (orderInfos []*biz.OrderInfo, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.CreateOrder")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "CreateOrder", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetCoreQuery(ctx)
	timeoutTime := time.Now().Add(-time.Duration(timeoutMinutes) * time.Minute)

	orders, err := q.SeckillOrder.WithContext(ctx).
		Where(
			q.SeckillOrder.Status.Eq(0),
			q.SeckillOrder.CreateTime.Lte(timeoutTime),
		).
		Find()

	if err != nil {
		return nil, err
	}

	result := make([]*biz.OrderInfo, 0, len(orders))
	for _, order := range orders {
		quantity := int64(0)
		if order.Quantity != nil {
			quantity = int64(*order.Quantity)
		}

		couponID := uint64(0)
		if order.CouponID != nil {
			couponID = *order.CouponID
		}

		createTime := ""
		if order.CreateTime != nil {
			createTime = order.CreateTime.Format("2006-01-02 15:04:05")
		}

		result = append(result, &biz.OrderInfo{
			OrderNo:     order.OrderNo,
			UserID:      order.UserID,
			SkuID:       order.SkuID,
			Quantity:    quantity,
			OrderAmount: order.OrderAmount,
			Status:      int32(order.Status),
			CreateTime:  createTime,
			CouponID:    couponID,
		})
	}

	return result, nil
}

func (r *mysqlRepo) GetPayInfoByPlatformNumber(ctx context.Context, platformNumber string) (res *biz.PayInfo, err error) {
	q := r.data.GetPayQuery(ctx)
	payInfo, err := q.PayInfo.WithContext(ctx).
		Where(q.PayInfo.PlatformNumber.Eq(platformNumber)).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrPayInfoNotFound
		}
		return nil, err
	}

	return &biz.PayInfo{
		OrderNo:        payInfo.OrderNo,
		UserID:         payInfo.UserID,
		PayPlatform:    payInfo.PayPlatform,
		PlatformNumber: payInfo.PlatformNumber,
		PlatformStatus: payInfo.PlatformStatus,
		PayAmount:      payInfo.PayAmount,
		PayTime:        payInfo.PayTime,
	}, nil
}

func (r *mysqlRepo) GetPayInfoByOrderNo(ctx context.Context, orderNo string) (res *biz.PayInfo, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.GetPayInfoByOrderNo")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "GetPayInfoByOrderNo", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetPayQuery(ctx)

	payInfo, err := q.PayInfo.WithContext(ctx).
		Where(q.PayInfo.OrderNo.Eq(orderNo)).
		First()

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrPayInfoNotFound
		}
		return nil, err
	}

	return &biz.PayInfo{
		OrderNo:        payInfo.OrderNo,
		UserID:         payInfo.UserID,
		PayPlatform:    payInfo.PayPlatform,
		PlatformNumber: payInfo.PlatformNumber,
		PlatformStatus: payInfo.PlatformStatus,
		PayAmount:      payInfo.PayAmount,
		PayTime:        payInfo.PayTime,
	}, nil
}

func (r *mysqlRepo) UpdatePayInfoStatus(ctx context.Context, platformNumber string, status string, payTime *time.Time) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.UpdatePayInfoStatus")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "UpdatePayInfoStatus", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetPayQuery(ctx)

	updates := map[string]interface{}{
		"platform_status": status,
	}

	if payTime != nil {
		updates["pay_time"] = payTime
	}

	info, err := q.PayInfo.WithContext(ctx).
		Where(q.PayInfo.PlatformNumber.Eq(platformNumber)).
		Updates(updates)

	if err != nil {
		return err
	}

	if info.RowsAffected == 0 {
		return biz.ErrPayInfoNotFound
	}

	return nil
}

func (r *mysqlRepo) CreateDeadLetterMessage(ctx context.Context, msg *biz.DeadLetterMessage) (err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "repo.mysql.CreateDeadLetterMessage")
	defer func() {
		observability.Finish(span, err)
		observability.ObserveOperation("mysql", "CreateDeadLetterMessage", func() string {
			if err != nil {
				return "fail"
			}
			return "success"
		}(), start)
	}()

	q := r.data.GetPayQuery(ctx)

	model := &payModel.MqDeadLetterMessage{
		EventID:      msg.EventID,
		Topic:        msg.Topic,
		PartitionID:  msg.Partition,
		OffsetID:     msg.Offset,
		OrderNo:      msg.OrderNo,
		RequestID:    msg.RequestID,
		TraceID:      msg.TraceID,
		RetryCount:   int32(msg.RetryCount),
		ErrorMessage: msg.ErrorMessage,
		RawPayload:   msg.RawPayload,
		Status:       &msg.Status,
	}

	err = q.MqDeadLetterMessage.WithContext(ctx).Create(model)
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) ||
			(err.Error() != "" && strings.Contains(err.Error(), "Duplicate entry")) {
			return nil
		}
		return err
	}

	return nil
}

func (r *mysqlRepo) GetDeadLetterMessageByEventID(ctx context.Context, eventID string) (*biz.DeadLetterMessage, error) {
	q := r.data.GetPayQuery(ctx)

	row, err := q.MqDeadLetterMessage.WithContext(ctx).
		Where(q.MqDeadLetterMessage.EventID.Eq(eventID)).
		First()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrDeadLetterNotFound
		}
		return nil, err
	}
	status := ""
	if row.Status != nil {
		status = *row.Status
	}

	var createTime time.Time
	if row.CreateTime != nil {
		createTime = *row.CreateTime
	}

	var updateTime time.Time
	if row.UpdateTime != nil {
		updateTime = *row.UpdateTime
	}
	return &biz.DeadLetterMessage{
		ID:           row.ID,
		EventID:      row.EventID,
		Topic:        row.Topic,
		Partition:    row.PartitionID,
		Offset:       row.OffsetID,
		OrderNo:      row.OrderNo,
		RequestID:    row.RequestID,
		TraceID:      row.TraceID,
		RetryCount:   int(row.RetryCount),
		ErrorMessage: row.ErrorMessage,
		RawPayload:   row.RawPayload,
		Status:       status,
		CreateTime:   createTime,
		UpdateTime:   updateTime,
	}, nil
}

func (r *mysqlRepo) ListDeadLetterMessages(ctx context.Context, status string, limit int) ([]*biz.DeadLetterMessage, error) {
	q := r.data.GetPayQuery(ctx)

	query := q.MqDeadLetterMessage.WithContext(ctx).Order(q.MqDeadLetterMessage.ID.Desc())
	if status != "" {
		query = query.Where(q.MqDeadLetterMessage.Status.Eq(status))
	}
	if limit > 0 {
		query = query.Limit(limit)
	}

	rows, err := query.Find()
	if err != nil {
		return nil, err
	}

	res := make([]*biz.DeadLetterMessage, 0, len(rows))
	for _, row := range rows {
		status := ""
		if row.Status != nil {
			status = *row.Status
		}

		var createTime time.Time
		if row.CreateTime != nil {
			createTime = *row.CreateTime
		}

		var updateTime time.Time
		if row.UpdateTime != nil {
			updateTime = *row.UpdateTime
		}

		res = append(res, &biz.DeadLetterMessage{
			ID:           row.ID,
			EventID:      row.EventID,
			Topic:        row.Topic,
			Partition:    row.PartitionID,
			Offset:       row.OffsetID,
			OrderNo:      row.OrderNo,
			RequestID:    row.RequestID,
			TraceID:      row.TraceID,
			RetryCount:   int(row.RetryCount),
			ErrorMessage: row.ErrorMessage,
			RawPayload:   row.RawPayload,
			Status:       status,
			CreateTime:   createTime,
			UpdateTime:   updateTime,
		})
	}

	return res, nil
}

func (r *mysqlRepo) MarkDeadLetterReplayed(ctx context.Context, eventID string) error {
	q := r.data.GetPayQuery(ctx)

	info, err := q.MqDeadLetterMessage.WithContext(ctx).
		Where(q.MqDeadLetterMessage.EventID.Eq(eventID)).
		UpdateSimple(q.MqDeadLetterMessage.Status.Value("REPLAYED"))
	if err != nil {
		return err
	}
	if info.RowsAffected == 0 {
		return biz.ErrDeadLetterNotFound
	}
	return nil
}
