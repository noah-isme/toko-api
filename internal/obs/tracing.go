package obs

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TracingConfig controls tracer provider initialisation.
type TracingConfig struct {
	ServiceName   string
	Endpoint      string
	Exporter      string
	SamplingRatio float64
	Environment   string
}

// InitTracer initialises the global tracer provider and returns a shutdown function.
func InitTracer(ctx context.Context, cfg TracingConfig) (func(context.Context) error, error) {
	exporter := strings.ToLower(strings.TrimSpace(cfg.Exporter))
	if exporter == "" {
		exporter = "otlp"
	}
	var (
		spanExporter sdktrace.SpanExporter
		err          error
	)
	switch exporter {
	case "otlp":
		opts := []otlptracehttp.Option{}
		if strings.TrimSpace(cfg.Endpoint) != "" {
			opts = append(opts, otlptracehttp.WithEndpointURL(cfg.Endpoint))
		}
		spanExporter, err = otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unsupported tracing exporter: %s", exporter)
	}
	if err != nil {
		return nil, err
	}
	ratio := cfg.SamplingRatio
	if ratio <= 0 {
		ratio = 1
	}
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.DeploymentEnvironmentKey.String(cfg.Environment),
		),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(ratio)),
		sdktrace.WithBatcher(spanExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
