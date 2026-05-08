package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

var Registry = prometheus.NewRegistry()

var (
	OpsTotal = prometheus.NewCounterVec( // 操作计数，QPS、成功/失败率
		prometheus.CounterOpts{
			Name: "seckill_ops_total",
			Help: "total ops count",
		},
		[]string{"component", "operation", "result"},
	)

	OpDuration = prometheus.NewHistogramVec( // 操作耗时分布
		prometheus.HistogramOpts{
			Name:    "seckill_op_duration_seconds",
			Help:    "operation duration",
			Buckets: prometheus.DefBuckets, // 默认桶：[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]
		},
		[]string{"component", "operation"},
	)

	InFlight = prometheus.NewGaugeVec( // 正在操作数，检测积压和限流
		prometheus.GaugeOpts{
			Name: "seckill_in_flight",
			Help: "in flight ops",
		},
		[]string{"component", "operation"},
	)

	QueueLag = prometheus.NewGaugeVec( // 监控消息/延迟队列的堆积程度
		prometheus.GaugeOpts{
			Name: "seckill_queue_lag_seconds",
			Help: "queue lag seconds",
		},
		[]string{"queue"},
	)
)

func init() {
	Registry.MustRegister(OpsTotal, OpDuration, InFlight, QueueLag)
}

// 封装两个常用指标采集（计算+耗时）
func ObserveOperation(component, operation string, result string, start time.Time) {
	OpsTotal.WithLabelValues(component, operation, result).Inc()
	OpDuration.WithLabelValues(component, operation).Observe(time.Since(start).Seconds())
}
