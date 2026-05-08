package kafka

import (
	"errors"
	"net"
	"seckill-service/internal/biz"
	"strings"
)

type failureType int

const (
	failureRetry failureType = iota
	failureDLQ
	failureDrop
)

func classifyOrderError(err error) failureType {
	switch {
	case err == nil:
		return failureDrop

	case errors.Is(err, biz.ErrOrderExists):
		// 幂等成功，直接提交 offset
		return failureDrop

	case errors.Is(err, biz.ErrSystemBusy),
		strings.Contains(err.Error(), "timeout"),
		strings.Contains(err.Error(), "deadlock"),
		strings.Contains(err.Error(), "connection refused"):
		return failureRetry

	case errors.Is(err, biz.ErrInsufficientStock),
		errors.Is(err, biz.ErrAddressNotFound),
		errors.Is(err, biz.ErrCouponInvalid),
		errors.Is(err, biz.ErrAlreadyBought),
		errors.Is(err, biz.ErrExceedLimit),
		errors.Is(err, biz.ErrOrderStatusIncorrect),
		errors.Is(err, biz.ErrOrderTimeout),
		errors.Is(err, biz.ErrPaymentExists),
		errors.Is(err, biz.ErrUserNotMatch):
		return failureDLQ

	default:
		if isTemporaryNetError(err) {
			return failureRetry
		}
		return failureDLQ
	}
}

func isTemporaryNetError(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout() || ne.Temporary()
	}
	return false
}
