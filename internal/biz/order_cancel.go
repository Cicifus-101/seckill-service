package biz

import (
	"context"
	"fmt"

	"github.com/go-kratos/kratos/v2/log"
)

type OrderCancelService struct {
	repo  SeckillRepo
	cache CacheRepo
	tx    Transaction
	log   *log.Helper
}

func NewOrderCancelService(repo SeckillRepo, cache CacheRepo, tx Transaction, logger log.Logger) *OrderCancelService {
	return &OrderCancelService{
		repo:  repo,
		cache: cache,
		tx:    tx,
		log:   log.NewHelper(log.With(logger, "module", "biz/order_cancel")),
	}
}

func (s *OrderCancelService) CancelTimeoutOrder(ctx context.Context, orderNo string, reason string) error {
	var order *OrderInfo

	err := s.tx.ExecTx(ctx, func(txCtx context.Context) error {
		var err error

		order, err = s.repo.GetOrderForUpdate(txCtx, orderNo)
		if err != nil {
			return fmt.Errorf("获取订单失败: %w", err)
		}

		if order.Status != OrderStatusPending {
			return nil
		}

		if err := s.repo.UpdateOrderStatus(txCtx, orderNo, OrderStatusPending, OrderStatusCancel); err != nil {
			return fmt.Errorf("更新订单状态失败: %w", err)
		}

		if err := s.repo.RestoreStock(txCtx, order.SkuID, uint32(order.Quantity)); err != nil {
			return fmt.Errorf("恢复 MySQL 库存失败: %w", err)
		}

		if order.CouponID > 0 {
			if err := s.repo.RestoreUserCoupon(txCtx, order.CouponID); err != nil {
				return fmt.Errorf("恢复优惠券失败: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if order == nil || order.Status != OrderStatusPending {
		return nil
	}

	if err := s.cache.RollbackStock(ctx, order.ActivityID, order.SkuID, int(order.Quantity)); err != nil {
		s.log.WithContext(ctx).Warnf("恢复 Redis 库存失败，等待库存补偿: orderNo=%s err=%v", orderNo, err)
	}

	if err := s.cache.RemoveUserBuy(ctx, order.ActivityID, order.SkuID, order.UserID); err != nil {
		s.log.WithContext(ctx).Warnf("删除用户购买标记失败: orderNo=%s err=%v", orderNo, err)
	}

	s.log.WithContext(ctx).Infof("订单取消成功: orderNo=%s reason=%s", orderNo, reason)
	return nil
}
