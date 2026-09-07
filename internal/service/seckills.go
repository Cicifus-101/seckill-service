package service

import (
	"context"
	stderrors "errors"
	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v1 "seckill-service/api/seckill/v1"
	"seckill-service/internal/biz"
	"seckill-service/internal/observability"
	"strings"
	"time"
)

type SeckillService struct {
	v1.UnimplementedSeckillServer
	uc  *biz.SeckillUsecase
	log *log.Helper
}

func NewSeckillService(uc *biz.SeckillUsecase, logger log.Logger) *SeckillService {
	return &SeckillService{
		uc:  uc,
		log: log.NewHelper(log.With(logger, "module", "service/seckill")),
	}
}

// SeckillProducts 查询秒杀商品列表
func (s *SeckillService) SeckillProducts(ctx context.Context, req *v1.SeckillProductsRequest) (_ *v1.SeckillProductsResponse, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.SeckillProducts")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "SeckillProducts", result, start)
	}()

	s.log.WithContext(ctx).Infof("SeckillProducts trace_id=%s req=%+v", observability.TraceID(ctx), req)

	res, err := s.uc.ListSeckillProducts(ctx, req.UserId, req.ActivityId, req.Page, req.PageSize, req.SortType)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询失败: .%v", err)
	}
	resp := &v1.SeckillProductsResponse{
		Total:    res.Total,
		Page:     res.Page,
		PageSize: res.PageSize,
		Products: make([]*v1.ProductInfo, 0, len(res.Products)),
	}
	for _, p := range res.Products {
		resp.Products = append(resp.Products, &v1.ProductInfo{
			SkuId:          int64(p.SkuID),
			ProductId:      int64(p.ProductID),
			Name:           p.Name,
			MainImage:      p.MainImage,
			SeckillPrice:   int64(p.SeckillPrice),
			MarketPrice:    int64(p.MarketPrice),
			AvailableStock: p.AvailableStock,
			TotalStock:     p.TotalStock,
			LimitNum:       p.LimitNum,
			SaleRate:       p.SaleRate,
			UserHasBought:  p.UserHasBought,
		})
	}

	if res.Activity != nil {
		resp.Activity = &v1.ActivityInfo{
			Id:               int64(res.Activity.ID),
			Title:            res.Activity.Title,
			Description:      res.Activity.Description,
			StartTime:        res.Activity.StartTime,
			EndTime:          res.Activity.EndTime,
			Status:           int32(res.Activity.Status),
			RemainingSeconds: res.Activity.RemainingSeconds,
		}
	}

	return resp, nil
}

// SeckillProductDetail 查询秒杀商品详情
func (s *SeckillService) SeckillProductDetail(ctx context.Context, req *v1.SeckillProductDetailRequest) (_ *v1.SeckillProductDetailResponse, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.SeckillProductDetail")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "SeckillProductDetail", result, start)
	}()

	s.log.WithContext(ctx).Infof("SeckillProductDetail trace_id=%s req=%+v", observability.TraceID(ctx), req)

	res, err := s.uc.GetSeckillProductDetail(ctx, uint64(req.UserId), uint64(req.ProductId), uint64(req.ActivityId))
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "商品不存在: %v", err)
	}
	resp := &v1.SeckillProductDetailResponse{
		Product: &v1.ProductDetail{
			ProductId:           int64(res.Product.ProductID),
			Name:                res.Product.Name,
			Subtitle:            res.Product.Subtitle,
			MainImage:           res.Product.MainImage,
			Detail:              res.Product.Detail,
			SkuId:               int64(res.Product.SkuID),
			SeckillPrice:        int64(res.Product.SeckillPrice),
			MarketPrice:         int64(res.Product.MarketPrice),
			TotalStock:          res.Product.TotalStock,
			AvailableStock:      res.Product.AvailableStock,
			LimitNum:            res.Product.LimitNum,
			ActivityId:          int64(res.Product.ActivityID),
			ActivityTitle:       res.Product.ActivityTitle,
			ActivityDescription: res.Product.Description,
			StartTime:           res.Product.StartTime,
			EndTime:             res.Product.EndTime,
			ActivityStatus:      res.Product.ActivityStatus,
			RemainingSeconds:    res.Product.RemainingSeconds,
		},
		AvailableCoupons: make([]*v1.CouponInfo, 0),
	}
	if res.UserStatus != nil {
		resp.UserStatus = &v1.UserSeckillStatus{
			HasBought:      res.UserStatus.HasBought,
			BoughtQuantity: res.UserStatus.BoughtQuantity,
			CanBuy:         res.UserStatus.CanBuy,
			RemainingLimit: res.UserStatus.RemainingLimit,
			Message:        res.UserStatus.Message,
		}
	}
	return resp, nil
}

// GetCurrentActivity 获取当前活动
func (s *SeckillService) GetCurrentActivity(ctx context.Context, req *v1.GetCurrentActivityRequest) (_ *v1.GetCurrentActivityResponse, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.GetCurrentActivity")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "GetCurrentActivity", result, start)
	}()

	s.log.WithContext(ctx).Infof("GetCurrentActivity trace_id=%s req=%+v", observability.TraceID(ctx), req)

	activity, productCount, err := s.uc.Repo.GetCurrentActivity(ctx)
	if err != nil {
		if errors.Is(err, biz.ErrNoActiveActivity) {
			return &v1.GetCurrentActivityResponse{
				Activity:     nil,
				ProductCount: 0,
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "查询失败: %v", err)
	}
	return &v1.GetCurrentActivityResponse{
		Activity: &v1.ActivityInfo{
			Id:               int64(activity.ID),
			Title:            activity.Title,
			Description:      activity.Description,
			StartTime:        activity.StartTime,
			EndTime:          activity.EndTime,
			Status:           activity.Status,
			RemainingSeconds: activity.RemainingSeconds,
		},
		ProductCount: productCount,
	}, nil
}

// CreateSeckillOrder 创建秒杀订单
func (s *SeckillService) CreateSeckillOrder(ctx context.Context, req *v1.CreateSeckillOrderRequest) (_ *v1.CreateSeckillOrderResponse, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.CreateSeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "CreateSeckillOrder", result, start)
	}()

	s.log.WithContext(ctx).Infof("CreateSeckillOrder trace_id=%s req=%+v", observability.TraceID(ctx), req)

	// 参数校验
	if req.UserId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "用户ID不能为空")
	}
	if req.SkuId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "商品SKU不能为空")
	}
	if req.AddressId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "收货地址不能为空")
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}

	res, err := s.uc.CreateSeckillOrder(ctx, &biz.CreateOrderRequest{
		UserID:     uint64(req.UserId),
		SkuID:      uint64(req.SkuId),
		ActivityID: uint64(req.ActivityId),
		ProductID:  uint64(req.ProductId),
		Quantity:   req.Quantity,
		CouponID:   uint64(req.CouponId),
		AddressID:  uint64(req.AddressId),
		ClientIP:   req.ClientIp,
		RequestID:  req.RequestId,
	})

	if err != nil {
		switch {
		case errors.Is(err, biz.ErrInsufficientStock):
			return nil, status.Errorf(codes.ResourceExhausted, "库存不足")
		case errors.Is(err, biz.ErrOrderExists):
			return nil, status.Errorf(codes.AlreadyExists, "订单已存在")
		case errors.Is(err, biz.ErrExceedLimit):
			return nil, status.Errorf(codes.FailedPrecondition, "超过限购数量")
		case errors.Is(err, biz.ErrActivityNotStart):
			return nil, status.Errorf(codes.FailedPrecondition, "活动未开始")
		case errors.Is(err, biz.ErrActivityEnded):
			return nil, status.Errorf(codes.FailedPrecondition, "活动已结束")
		default:
			return nil, status.Errorf(codes.Internal, "创建订单失败: %v", err)
		}
	}
	return &v1.CreateSeckillOrderResponse{
		OrderNo:          res.OrderNo,
		OrderAmount:      int64(res.OrderAmount),
		CouponDiscount:   int64(res.CouponDiscount),
		FinalAmount:      int64(res.FinalAmount),
		Status:           res.Status,
		SeckillPrice:     int64(res.SeckillPrice),
		Quantity:         res.Quantity,
		Message:          res.Message,
		RemainingSeconds: res.RemainingSeconds,
	}, nil
}

// GetSeckillOrder 查询秒杀订单
func (s *SeckillService) GetSeckillOrder(ctx context.Context, req *v1.GetSeckillOrderRequest) (_ *v1.GetSeckillOrderResponse, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.GetSeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "GetSeckillOrder", result, start)
	}()

	s.log.WithContext(ctx).Infof("GetSeckillOrder trace_id=%s req=%+v", observability.TraceID(ctx), req)

	order, err := s.uc.GetSeckillOrder(ctx, req.OrderNo, uint64(req.UserId))
	if err != nil {
		if stderrors.Is(err, biz.ErrOrderNotFound) {
			return nil, errors.NotFound("ORDER_NOT_FOUND", "订单不存在")
		}
		if stderrors.Is(err, biz.ErrUserNotMatch) {
			return nil, errors.Forbidden("USER_NOT_MATCH", "用户不匹配")
		}
		s.log.WithContext(ctx).Errorf("GetSeckillOrder failed: %v", err)
		return nil, errors.InternalServer("QUERY_ORDER_FAILED", "查询订单失败")
	}

	resp := &v1.GetSeckillOrderResponse{
		Order: &v1.SeckillOrderInfo{
			OrderNo:          order.OrderNo,
			UserId:           int64(order.UserID),
			ActivityId:       int64(order.ActivityID),
			ProductId:        int64(order.ProductID),
			SkuId:            int64(order.SkuID),
			ProductName:      order.ProductName,
			ProductImage:     order.ProductImage,
			SeckillPrice:     int64(order.SeckillPrice),
			Quantity:         order.Quantity,
			OrderAmount:      int64(order.OrderAmount),
			CouponId:         int64(order.CouponID),
			CouponDiscount:   int64(order.CouponDiscount),
			FinalAmount:      int64(order.FinalAmount),
			Status:           order.Status,
			CreateTime:       order.CreateTime,
			RemainingSeconds: order.RemainingSeconds,
		},
	}
	if order.Address != nil {
		resp.Order.Address = &v1.AddressSnapshot{
			Id:            0, // 快照表没有ID
			ReceiverName:  order.Address.ReceiverName,
			ReceiverPhone: order.Address.ReceiverPhone,
			Province:      order.Address.Province,
			City:          order.Address.City,
			District:      order.Address.District,
			DetailAddress: order.Address.DetailAddress,
		}
	}
	return resp, nil
}

// GetSeckillResult 获取秒杀结果
func (s *SeckillService) GetSeckillResult(ctx context.Context, req *v1.GetSeckillResultRequest) (_ *v1.GetSeckillResultResponse, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.GetSeckillResult")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "GetSeckillResult", result, start)
	}()

	s.log.WithContext(ctx).Infof("GetSeckillResult trace_id=%s req=%+v", observability.TraceID(ctx), req)

	res, err := s.uc.GetSeckillResult(ctx, uint64(req.UserId), req.RequestId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询失败: %v", err)
	}

	return &v1.GetSeckillResultResponse{
		Status:      res.Status,
		OrderNo:     res.OrderNo,
		Message:     res.Message,
		OrderAmount: int64(res.OrderAmount),
	}, nil
}

// PaySeckillOrder 支付秒杀订单
func (s *SeckillService) PaySeckillOrder(ctx context.Context, req *v1.PaySeckillOrderRequest) (_ *v1.PaySeckillOrderResponse, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.PaySeckillOrder")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "PaySeckillOrder", result, start)
	}()

	s.log.WithContext(ctx).Infof("PaySeckillOrder trace_id=%s req=%+v", observability.TraceID(ctx), req)

	result, err := s.uc.PaySeckillOrder(ctx, &biz.PayOrderRequest{
		OrderNo:     req.OrderNo,
		UserID:      uint64(req.UserId),
		PayPlatform: req.PayPlatform,
	})

	if err != nil {
		return nil, status.Errorf(codes.Internal, "支付失败: %v", err)
	}

	return &v1.PaySeckillOrderResponse{
		Success:        result.Success,
		Message:        result.Message,
		PayUrl:         "",
		PayAmount:      int64(result.PayAmount),
		PlatformNumber: result.PlatformNumber,
	}, nil
}

func (s *SeckillService) PayCallback(ctx context.Context, req *v1.PayCallbackRequest) (_ *v1.PayCallbackReply, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.PayCallback")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "PayCallback", result, start)
	}()

	s.log.WithContext(ctx).Infof("PayCallback trace_id=%s req=%+v", observability.TraceID(ctx), req)

	res, err := s.uc.HandlePayCallback(ctx, &biz.PayCallbackRequest{
		OrderNo:        req.OrderNo,
		PlatformNumber: req.PlatformNumber,
		PayAmount:      uint64(req.PayAmount),
		PlatformStatus: req.PlatformStatus,
		PayTime:        req.PayTime,
		Sign:           req.Sign,
	})

	if err != nil {
		return nil, mapPayCallbackError(err)
	}

	return &v1.PayCallbackReply{
		Success: res.Success,
		Message: res.Message,
	}, nil
}

func (s *SeckillService) GrantCouponByReview(ctx context.Context, req *v1.GrantCouponByReviewRequest) (_ *v1.GrantCouponByReviewReply, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.GrantCouponByReview")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "GrantCouponByReview", result, start)
	}()

	s.log.WithContext(ctx).Infof("GrantCouponByReview trace_id=%s req=%+v", observability.TraceID(ctx), req)
	if req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id不能为空")
	}
	scene := strings.ToUpper(strings.TrimSpace(req.Scene))
	if req.ReviewId == 0 && scene != biz.CouponSceneInviteNew && scene != biz.CouponSceneAfterSaleCompensation {
		return nil, status.Error(codes.InvalidArgument, "review_id不能为空")
	}
	if req.ReviewId > 0 && req.StoreId == 0 {
		return nil, status.Error(codes.InvalidArgument, "store_id不能为空")
	}

	res, err := s.uc.GrantCouponByReview(ctx, &biz.GrantCouponRequest{
		StoreID:          uint64(req.StoreId),
		UserID:           uint64(req.UserId),
		ReviewID:         uint64(req.ReviewId),
		OrderNo:          req.OrderNo,
		ProductID:        uint64(req.ProductId),
		Rating:           req.Rating,
		HasImage:         req.HasImage,
		IsFirstReview:    req.IsFirstReview,
		Scene:            scene,
		ActivityID:       strings.TrimSpace(req.ActivityId),
		PolicyVersion:    strings.TrimSpace(req.PolicyVersion),
		EvidenceVersion:  req.EvidenceVersion,
		CouponTemplateID: strings.TrimSpace(req.CouponTemplateId),
		IdempotencyKey:   req.IdempotencyKey,
	})
	if err != nil {
		switch {
		case stderrors.Is(err, biz.ErrCouponSceneInvalid):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case stderrors.Is(err, biz.ErrCouponInvalid):
			return nil, status.Error(codes.ResourceExhausted, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &v1.GrantCouponByReviewReply{
		Success:    true,
		Message:    res.Message,
		Coupon:     toProtoUserCoupon(res.Coupon),
		Duplicated: res.Duplicated,
	}, nil
}

func (s *SeckillService) ListUserCoupons(ctx context.Context, req *v1.ListUserCouponsRequest) (_ *v1.ListUserCouponsReply, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.ListUserCoupons")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "ListUserCoupons", result, start)
	}()

	s.log.WithContext(ctx).Infof("ListUserCoupons trace_id=%s req=%+v", observability.TraceID(ctx), req)
	coupons, total, err := s.uc.ListUserCoupons(ctx, uint64(req.UserId), req.Status, req.Page, req.PageSize)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	reply := &v1.ListUserCouponsReply{
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
		Coupons:  make([]*v1.UserCouponInfo, 0, len(coupons)),
	}
	if reply.Page <= 0 {
		reply.Page = 1
	}
	if reply.PageSize <= 0 {
		reply.PageSize = 20
	}
	for _, coupon := range coupons {
		reply.Coupons = append(reply.Coupons, toProtoUserCoupon(coupon))
	}
	return reply, nil
}

func toProtoUserCoupon(coupon *biz.UserCoupon) *v1.UserCouponInfo {
	if coupon == nil {
		return nil
	}
	return &v1.UserCouponInfo{
		Id:           int64(coupon.ID),
		CouponId:     int64(coupon.CouponID),
		Name:         coupon.Name,
		Type:         coupon.Type,
		Value:        int64(coupon.Value),
		MinAmount:    int64(coupon.MinAmount),
		Scene:        coupon.Scene,
		Status:       coupon.Status,
		ReceivedTime: coupon.ReceivedTime,
		UsedTime:     coupon.UsedTime,
		ExpireTime:   coupon.ExpireTime,
	}
}

func (s *SeckillService) UpdateSeckillActivity(ctx context.Context, req *v1.UpdateSeckillActivityRequest) (_ *v1.UpdateSeckillActivityReply, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.UpdateSeckillActivity")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "UpdateSeckillActivity", result, start)
	}()

	if req.ActivityId == 0 {
		return nil, status.Error(codes.InvalidArgument, "activity_id不能为空")
	}
	if err := s.uc.UpdateSeckillActivity(ctx, &biz.UpdateActivityRequest{
		ActivityID:  uint64(req.ActivityId),
		Title:       req.Title,
		Description: req.Description,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Status:      req.Status,
		WarmUp:      req.WarmUp,
	}); err != nil {
		return nil, mapCacheAsideUpdateError(err)
	}
	return &v1.UpdateSeckillActivityReply{Success: true, Message: "活动已更新，缓存已失效"}, nil
}

func (s *SeckillService) UpdateSeckillProduct(ctx context.Context, req *v1.UpdateSeckillProductRequest) (_ *v1.UpdateSeckillProductReply, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.UpdateSeckillProduct")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "UpdateSeckillProduct", result, start)
	}()

	if req.ActivityId == 0 || req.ProductId == 0 {
		return nil, status.Error(codes.InvalidArgument, "activity_id/product_id不能为空")
	}
	if req.ProductStatus <= 0 || req.SeckillPrice <= 0 || req.MarketPrice <= 0 || req.TotalStock <= 0 || req.AvailableStock < 0 || req.LimitNum <= 0 {
		return nil, status.Error(codes.InvalidArgument, "商品状态、价格、库存和限购参数不合法")
	}
	if err := s.uc.UpdateSeckillProduct(ctx, &biz.UpdateProductRequest{
		ActivityID:     uint64(req.ActivityId),
		ProductID:      uint64(req.ProductId),
		Name:           req.Name,
		Subtitle:       req.Subtitle,
		MainImage:      req.MainImage,
		Detail:         req.Detail,
		ProductStatus:  req.ProductStatus,
		SeckillPrice:   uint64(req.SeckillPrice),
		MarketPrice:    uint64(req.MarketPrice),
		TotalStock:     uint32(req.TotalStock),
		AvailableStock: uint32(req.AvailableStock),
		LimitNum:       uint32(req.LimitNum),
		WarmUp:         req.WarmUp,
	}); err != nil {
		return nil, mapCacheAsideUpdateError(err)
	}
	return &v1.UpdateSeckillProductReply{Success: true, Message: "商品已更新，缓存已失效"}, nil
}

func mapCacheAsideUpdateError(err error) error {
	switch {
	case stderrors.Is(err, biz.ErrInvalidCacheAsideUpdate):
		return status.Error(codes.InvalidArgument, err.Error())
	case stderrors.Is(err, biz.ErrActivityNotFound), stderrors.Is(err, biz.ErrProductNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func mapPayCallbackError(err error) error {
	switch {
	case stderrors.Is(err, biz.ErrInvalidPaySign):
		return status.Error(codes.PermissionDenied, err.Error())
	case stderrors.Is(err, biz.ErrPayInfoNotFound), stderrors.Is(err, biz.ErrOrderNotFound):
		return status.Error(codes.NotFound, err.Error())
	case stderrors.Is(err, biz.ErrPayAmountMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case stderrors.Is(err, biz.ErrOrderStatusIncorrect):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func (s *SeckillService) ReplayDeadLetter(ctx context.Context, req *v1.ReplayDeadLetterRequest) (_ *v1.ReplayDeadLetterReply, err error) {
	start := time.Now()
	ctx, span := observability.Start(ctx, "service.ReplayDeadLetter")
	defer func() {
		observability.Finish(span, err)
		result := "success"
		if err != nil {
			result = "fail"
		}
		observability.ObserveOperation("service", "ReplayDeadLetter", result, start)
	}()

	s.log.WithContext(ctx).Infof("ReplayDeadLetter trace_id=%s req=%+v", observability.TraceID(ctx), req)
	if req.EventId == "" {
		return nil, status.Error(codes.InvalidArgument, "event_id不能为空")
	}
	if err := s.uc.ReplayDeadLetter(ctx, req.EventId); err != nil {
		switch {
		case errors.Is(err, biz.ErrDeadLetterNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	return &v1.ReplayDeadLetterReply{
		Success: true,
		Message: "重放成功",
	}, nil
}
