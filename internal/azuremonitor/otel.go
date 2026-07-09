package azuremonitor

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace/noop"
)

type OTelOptions struct {
	ServiceName  string
	OTLPEndpoint string
}

type ShutdownFunc func(context.Context) error

func SetupOTel(ctx context.Context, options OTelOptions) (ShutdownFunc, error) {
	endpoint := strings.TrimSpace(options.OTLPEndpoint)
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	serviceName := strings.TrimSpace(options.ServiceName)
	if serviceName == "" {
		return nil, errors.New("otel service name is required")
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, err
	}
	res := resource.NewSchemaless(semconv.ServiceName(serviceName))
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(provider)

	return func(ctx context.Context) error {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return provider.Shutdown(ctx)
	}, nil
}
