package observability

import (
	"context"
	"net/http"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func StartMetricsServer(addr string, logger log.Logger) (func(context.Context) error, error) {
	if addr == "" {
		return func(context.Context) error { return nil }, nil
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(Registry, promhttp.HandlerOpts{}))

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	helper := log.NewHelper(log.With(logger, "module", "observability/metrics"))

	go func() {
		helper.Infof("metrics server started at %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			helper.Errorf("metrics server stopped with error: %v", err)
		}
	}()

	return srv.Shutdown, nil
}
