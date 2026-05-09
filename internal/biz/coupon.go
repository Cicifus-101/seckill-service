package biz

import (
	"context"
	"fmt"
	"strings"
)

const (
	CouponTypeCash     int32 = 1 // 代金券
	CouponTypeDiscount int32 = 2 // 折扣券

	UserCouponStatusUnused  int32 = 1
	UserCouponStatusUsed    int32 = 2
	UserCouponStatusExpired int32 = 3

	// agent 用户画像和推荐系统
	CouponSceneFirstReview           = "FIRST_REVIEW"
	CouponSceneGoodReview            = "GOOD_REVIEW"
	CouponScenePhotoReview           = "PHOTO_REVIEW"
	CouponSceneInviteNew             = "INVITE_NEW"
	CouponSceneAfterSaleCompensation = "AFTER_SALE_COMPENSATION"
)

// 发券规则配置
type CouponTemplate struct {
	Name      string
	Type      int32
	Value     uint64
	MinAmount uint64
	ValidDays int
}

func (uc *SeckillUsecase) GrantCouponByReview(ctx context.Context, req *GrantCouponRequest) (*GrantCouponResult, error) {
	if req.UserID == 0 {
		return nil, ErrUserNotMatch
	}
	scene, err := uc.resolveCouponScene(req)
	if err != nil {
		return nil, err
	}
	req.Scene = scene
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = fmt.Sprintf("review:%d:%s", req.ReviewID, scene)
	}

	template, ok := couponTemplateByScene(scene)
	if !ok {
		return nil, ErrCouponSceneInvalid
	}

	var userCoupon *UserCoupon
	var duplicated bool
	// 事务发券
	err = uc.tx.ExecTx(ctx, func(txCtx context.Context) error {
		var grantErr error
		userCoupon, duplicated, grantErr = uc.Repo.GrantUserCoupon(txCtx, req, template)
		return grantErr
	})
	if err != nil {
		return nil, err
	}

	message := "发券成功"
	if duplicated {
		message = "重复发券请求，返回已有优惠券"
	}
	return &GrantCouponResult{
		Coupon:     userCoupon,
		Duplicated: duplicated,
		Message:    message,
	}, nil
}

func (uc *SeckillUsecase) ListUserCoupons(ctx context.Context, userID uint64, status int32, page, pageSize int32) ([]*UserCoupon, int64, error) {
	if userID == 0 {
		return nil, 0, ErrUserNotMatch
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	return uc.Repo.ListUserCoupons(ctx, userID, status, page, pageSize)
}

func (uc *SeckillUsecase) resolveCouponScene(req *GrantCouponRequest) (string, error) {
	scene := strings.ToUpper(strings.TrimSpace(req.Scene))
	if scene != "" {
		if !uc.matchCouponScene(scene, req) {
			return "", ErrCouponSceneInvalid
		}
		return scene, nil
	}
	switch {
	case req.IsFirstReview:
		return CouponSceneFirstReview, nil
	case req.Rating >= 4 && req.HasImage:
		return CouponScenePhotoReview, nil
	case req.Rating >= 4:
		return CouponSceneGoodReview, nil
	default:
		return "", ErrCouponSceneInvalid
	}
}

func (uc *SeckillUsecase) matchCouponScene(scene string, req *GrantCouponRequest) bool {
	switch scene {
	case CouponSceneFirstReview:
		return req.IsFirstReview
	case CouponSceneGoodReview:
		return req.Rating >= 4
	case CouponScenePhotoReview:
		return req.HasImage
	case CouponSceneInviteNew, CouponSceneAfterSaleCompensation:
		return true
	default:
		return false
	}
}

// couponTemplateByScene 营销规则配置中心
func couponTemplateByScene(scene string) (*CouponTemplate, bool) {
	switch scene {
	case CouponSceneFirstReview:
		return &CouponTemplate{Name: "首评送5元券", Type: CouponTypeCash, Value: 500, MinAmount: 0, ValidDays: 30}, true
	case CouponSceneGoodReview:
		return &CouponTemplate{Name: "好评返95折券", Type: CouponTypeDiscount, Value: 95, MinAmount: 5000, ValidDays: 30}, true
	case CouponScenePhotoReview:
		return &CouponTemplate{Name: "晒图返3元券", Type: CouponTypeCash, Value: 300, MinAmount: 3000, ValidDays: 30}, true
	case CouponSceneInviteNew:
		return &CouponTemplate{Name: "拉新10元券", Type: CouponTypeCash, Value: 1000, MinAmount: 0, ValidDays: 30}, true
	case CouponSceneAfterSaleCompensation:
		return &CouponTemplate{Name: "售后补偿20元券", Type: CouponTypeCash, Value: 2000, MinAmount: 0, ValidDays: 30}, true
	default:
		return nil, false
	}
}
