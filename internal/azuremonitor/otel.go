package azuremonitor

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace/noop"
)

// OTelOptions carries the runtime observability inputs SetupOTel needs.
//
// OTLPEndpoint MUST NOT be set explicitly in Terraform or hand-authored
// deployment configuration. In Azure Container Apps the managed
// OpenTelemetry agent injects OTEL_EXPORTER_OTLP_ENDPOINT into every
// container in the environment automatically, pointing at the local
// in-cluster OTel gRPC sidecar. This code reads that runtime-injected
// value only and never publishes an explicit endpoint through the ACA
// module's environment variables.
type OTelOptions struct {
	ServiceName  string
	OTLPEndpoint string
}

type ShutdownFunc func(context.Context) error

// SetupOTel configures the OTLP gRPC trace exporter accepted by the
// ACA managed OpenTelemetry agent. When OTLPEndpoint is blank we
// return a no-op shutdown and do not install a tracer provider — the
// caller therefore does not need to gate the call on whether the
// managed agent is configured.
func SetupOTel(ctx context.Context, options OTelOptions) (ShutdownFunc, error) {
	endpoint := strings.TrimSpace(options.OTLPEndpoint)
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	serviceName := strings.TrimSpace(options.ServiceName)
	if serviceName == "" {
		return nil, errors.New("otel service name is required")
	}

	// WithEndpointURL accepts a full URL ("http://host:port" or
	// "https://host:port"). The scheme in the injected endpoint
	// determines TLS behaviour: the ACA managed agent injects an
	// http:// URL because the sidecar is a local, same-pod endpoint,
	// which turns off client TLS as intended. New is non-blocking; a
	// bad endpoint surfaces later at export time, not at construction.
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(endpoint))
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
