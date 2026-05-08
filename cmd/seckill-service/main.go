package main

import (
	"context"
	"flag"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"os"
	"seckill-service/internal/conf"
	"seckill-service/internal/observability"
)

var (
	Name     = "seckill-service"
	Version  string
	flagconf string

	id, _ = os.Hostname()
)

func init() {
	flag.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf config.yaml")
}

func main() {
	flag.Parse()

	// 初始化 logger
	logger := log.With(
		log.NewStdLogger(os.Stdout),
		"ts", log.DefaultTimestamp,
		"caller", log.DefaultCaller,
		"service.id", id,
		"service.name", Name,
		"service.version", Version,
		"trace.id", tracing.TraceID(),
		"span.id", tracing.SpanID(),
	)

	// 加载配置
	c := config.New(
		config.WithSource(
			file.NewSource(flagconf),
		),
	)
	defer c.Close()

	if err := c.Load(); err != nil {
		panic(err)
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		panic(err)
	}

	// 初始化 OpenTelemetry
	obs := bc.GetObservability()
	if obs != nil {
		// 初始化 tracer
		shutdown, err := observability.InitTracer(context.Background(), observability.TracerConfig{
			Enabled:     obs.GetEnabled(),
			ServiceName: obs.GetServiceName(),
			Env:         obs.GetEnv(),
			Endpoint:    obs.GetOtelEndpoint(),
			SampleRatio: obs.GetSampleRatio(),
		})
		if err != nil {
			panic(err)
		}
		defer func() { _ = shutdown(context.Background()) }()

		// 启动 matrics server
		shutdownMetrics, err := observability.StartMetricsServer(
			obs.GetMetricsAddr(),
			logger,
		)
		if err != nil {
			panic(err)
		}
		defer func() { _ = shutdownMetrics(context.Background()) }()
	}

	// 初始化应用
	app, cleanup, err := wireApp(bc.Server, bc.Data, bc.Kafka, logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()

	// 启动并阻塞，直到收到退出信号
	if err := app.Run(); err != nil {
		panic(err)
	}
}
