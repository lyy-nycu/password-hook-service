package azuremonitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
		OTLPEndpoint: "http://localhost:4318",
	})
	if err == nil || err.Error() != "otel service name is required" {
		t.Fatalf("SetupOTel error = %v, want service name error", err)
	}
	if shutdown != nil {
		t.Fatalf("SetupOTel shutdown = %#v, want nil", shutdown)
	}
}

func TestSetupOTelReturnsShutdownForEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	shutdown, err := SetupOTel(context.Background(), OTelOptions{
		ServiceName:  "password-hook-service",
		OTLPEndpoint: server.URL,
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
