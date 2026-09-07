package observability

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
)

// TraceMiddleware 为每一个rpc请求创建一个追踪Span
func TraceMiddleware(component string) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (reply interface{}, err error) {
			op := fmt.Sprintf("%T", req) //反射动态生成操作总名称
			ctx, span := Start(ctx, component+"."+op)
			defer func() {
				// 如果业务逻辑执行出错，finish会将错误信息附加到span中（无论业务曾公告失败，Span都能被正确结束）
				Finish(span, err)
			}()
			return next(ctx, req)
		}
	}
}

// MetricsMiddleware 统计每个请求的QPS、耗时和并发数，用于监控告警
func MetricsMiddleware(component string) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (reply interface{}, err error) {
			op := fmt.Sprintf("%T", req)
			start := time.Now()
			g := InFlight.WithLabelValues(component, op) //统计并发请求数，为特定的（服务，操作）打上标签
			g.Inc()                                      // 请求进来，并发数+1
			defer g.Dec()

			reply, err = next(ctx, req)

			result := "success"
			if err != nil {
				result = "fail"
			}
			ObserveOperation(component, op, result, start) //记录请求总数和耗时分布
			return reply, err
		}
	}
}
