package azuremonitor

import (
	"context"
	"testing"
	"time"
)

func TestSetupOTelNoopsWhenEndpointEmpty(t *testing.T) {
	shutdown, err := SetupOTel(context.Background(), OTelOptions{
		ServiceName:  "password-hook-service",
		OTLPEndpoint: " \t ",
	})
	if err != nil {
		t.Fatalf("SetupOTel returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("SetupOTel returned nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}

func TestSetupOTelRequiresServiceNameWhenEndpointConfigured(t *testing.T) {
	shutdown, err := SetupOTel(context.Background(), OTelOptions{
		OTLPEndpoint: "http://localhost:4317",
	})
	if err == nil || err.Error() != "otel service name is required" {
		t.Fatalf("SetupOTel error = %v, want service name error", err)
	}
	if shutdown != nil {
		t.Fatalf("SetupOTel shutdown = %#v, want nil", shutdown)
	}
}

// TestSetupOTelAcceptsInjectedInsecureEndpoint verifies SetupOTel handles the
// http://host:port form the ACA managed OpenTelemetry agent injects via
// OTEL_EXPORTER_OTLP_ENDPOINT — the scheme selects insecure gRPC transport
// against the local same-pod sidecar. The exporter constructor is non-
// blocking; we assert clean shutdown does not hang or return an error.
func TestSetupOTelAcceptsInjectedInsecureEndpoint(t *testing.T) {
	shutdown, err := SetupOTel(context.Background(), OTelOptions{
		ServiceName:  "password-hook-service",
		OTLPEndpoint: "http://localhost:4317",
	})
	if err != nil {
		t.Fatalf("SetupOTel returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("SetupOTel returned nil shutdown")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}

// TestSetupOTelAcceptsSecureEndpointURL covers the https:// form: the SDK
// still constructs the exporter successfully (New is non-blocking) and
// shutdown cleans up without leaking goroutines. This exercises the second
// scheme branch of WithEndpointURL for parity with the insecure case above.
func TestSetupOTelAcceptsSecureEndpointURL(t *testing.T) {
	shutdown, err := SetupOTel(context.Background(), OTelOptions{
		ServiceName:  "password-hook-service",
		OTLPEndpoint: "https://otel-collector.internal:4317",
	})
	if err != nil {
		t.Fatalf("SetupOTel returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("SetupOTel returned nil shutdown")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}
