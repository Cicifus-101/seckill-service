package observability

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
)

// TraceMiddleware 为每一个请求创建一个追踪Span
func TraceMiddleware(component string) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (reply interface{}, err error) {
			op := fmt.Sprintf("%T", req)
			ctx, span := Start(ctx, component+"."+op)
			defer func() {
				Finish(span, err)
			}()
			return next(ctx, req)
		}
	}
}

// MetricsMiddleware 统计每个请求的QPS、耗时和并发数
func MetricsMiddleware(component string) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (reply interface{}, err error) {
			op := fmt.Sprintf("%T", req)
			start := time.Now()
			g := InFlight.WithLabelValues(component, op)
			g.Inc()
			defer g.Dec()

			reply, err = next(ctx, req)

			result := "success"
			if err != nil {
				result = "fail"
			}
			ObserveOperation(component, op, result, start)
			return reply, err
		}
	}
}
