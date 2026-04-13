package service

import (
	"context"
	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v1 "seckill-service/api/seckill/v1"
	"seckill-service/internal/biz"
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
func (s *SeckillService) SeckillProducts(ctx context.Context, req *v1.SeckillProductsRequest) (*v1.SeckillProductsResponse, error) {
	s.log.WithContext(ctx).Infof("SeckillProducts req: %+v", req)

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
func (s *SeckillService) SeckillProductDetail(ctx context.Context, req *v1.SeckillProductDetailRequest) (*v1.SeckillProductDetailResponse, error) {
	s.log.WithContext(ctx).Infof("SeckillProductDetail req: %+v", req)
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
func (s *SeckillService) GetCurrentActivity(ctx context.Context, req *v1.GetCurrentActivityRequest) (*v1.GetCurrentActivityResponse, error) {
	s.log.WithContext(ctx).Info("GetCurrentActivity")
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
func (s *SeckillService) CreateSeckillOrder(ctx context.Context, req *v1.CreateSeckillOrderRequest) (*v1.CreateSeckillOrderResponse, error) {
	s.log.WithContext(ctx).Infof("CreateSeckillOrder req: %+v", req)

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
func (s *SeckillService) GetSeckillOrder(ctx context.Context, req *v1.GetSeckillOrderRequest) (*v1.GetSeckillOrderResponse, error) {
	s.log.WithContext(ctx).Infof("GetSeckillOrder req: %+v", req)
	order, err := s.uc.GetSeckillOrder(ctx, req.OrderNo, uint64(req.UserId))
	if err != nil {
		if errors.Is(err, biz.ErrOrderNotFound) {
			return nil, status.Errorf(codes.NotFound, "订单不存在")
		}
		return nil, status.Errorf(codes.Internal, "查询失败: %v", err)
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
func (s *SeckillService) GetSeckillResult(ctx context.Context, req *v1.GetSeckillResultRequest) (*v1.GetSeckillResultResponse, error) {
	s.log.WithContext(ctx).Infof("GetSeckillResult req: %+v", req)

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
func (s *SeckillService) PaySeckillOrder(ctx context.Context, req *v1.PaySeckillOrderRequest) (*v1.PaySeckillOrderResponse, error) {
	s.log.WithContext(ctx).Infof("PaySeckillOrder req: %+v", req)

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
