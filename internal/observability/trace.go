package observability

import (
	"context"
	"github.com/IBM/sarama"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"strings"
)

var tracer = otel.Tracer("seckill-service")

// 不同系统之间传递 trace 信息
type MapCarrier map[string]string

func (c MapCarrier) Get(key string) string {
	return c[strings.ToLower(key)]
}

func (c MapCarrier) Set(key, val string) {
	c[strings.ToLower(key)] = val
}

func (c MapCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

func Finish(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "ok")
	}
	span.End()
}

func TraceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}

func InjectKafkaHeaders(ctx context.Context) map[string]string {
	carrier := MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return map[string]string(carrier)
}

func ExtractKafkaContext(ctx context.Context, headers []*sarama.RecordHeader) context.Context {
	carrier := MapCarrier{}
	for _, h := range headers {
		carrier[strings.ToLower(string(h.Key))] = string(h.Value)
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
