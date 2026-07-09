package azuremonitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nycu/password-hook-service/internal/observability"
)

func TestMetricRecorderPublishesCustomMetricWithoutSecrets(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	recorder := NewMetricRecorder(MetricRecorderOptions{
		EndpointBaseURL: server.URL,
		ResourceID:      "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/password-hook",
		Region:          "eastasia",
		Namespace:       "password-hook-service",
		TokenSource:     staticTokenSource("token"),
		HTTPClient:      server.Client(),
		Now:             func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) },
	})

	recorder.Inc(context.Background(), observability.MetricHookRequestsTotal, observability.Labels{
		"status":             "202",
		"outcome":            "enqueued",
		"passwordCiphertext": "must-not-appear",
		"traceId":            "trace-123",
	})
	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if gotAuth != "Bearer token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	body, _ := json.Marshal(gotBody)
	if !strings.Contains(string(body), observability.MetricHookRequestsTotal) {
		t.Fatalf("body = %s, want metric name", body)
	}
	for _, forbidden := range []string{"cleartext-password", "passwordCiphertext", "passwordNonce", "Authorization", "trace-123", "must-not-appear"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("custom metric body leaked forbidden value %q: %s", forbidden, body)
		}
	}
}

func TestMetricRecorderAggregatesMatchingMetricAndLabels(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	recorder := NewMetricRecorder(MetricRecorderOptions{
		EndpointBaseURL: server.URL,
		ResourceID:      "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/password-hook",
		Region:          "eastasia",
		Namespace:       "password-hook-service",
		TokenSource:     staticTokenSource("token"),
		HTTPClient:      server.Client(),
		Now:             func() time.Time { return time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC) },
	})

	labels := observability.Labels{"outcome": "success"}
	recorder.ObserveDuration(context.Background(), observability.MetricGraphUpsertDuration, 1500*time.Millisecond, labels)
	recorder.ObserveDuration(context.Background(), observability.MetricGraphUpsertDuration, 500*time.Millisecond, labels)

	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	series := firstSeries(t, gotBody)
	if got := series["min"]; got != float64(0.5) {
		t.Fatalf("min = %#v, want 0.5", got)
	}
	if got := series["max"]; got != float64(1.5) {
		t.Fatalf("max = %#v, want 1.5", got)
	}
	if got := series["sum"]; got != float64(2) {
		t.Fatalf("sum = %#v, want 2", got)
	}
	if got := series["count"]; got != float64(2) {
		t.Fatalf("count = %#v, want 2", got)
	}
}

func TestMetricRecorderFlushNoopsWithoutPoints(t *testing.T) {
	recorder := NewMetricRecorder(MetricRecorderOptions{
		TokenSource: staticTokenSource("token"),
	})

	if err := recorder.Flush(context.Background()); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
}

type staticTokenSource string

func (s staticTokenSource) Token(context.Context) (string, error) {
	return string(s), nil
}

func firstSeries(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("body[data] = %#v", body["data"])
	}
	baseData, ok := data["baseData"].(map[string]any)
	if !ok {
		t.Fatalf("data[baseData] = %#v", data["baseData"])
	}
	series, ok := baseData["series"].([]any)
	if !ok || len(series) != 1 {
		t.Fatalf("baseData[series] = %#v", baseData["series"])
	}
	got, ok := series[0].(map[string]any)
	if !ok {
		t.Fatalf("series[0] = %#v", series[0])
	}
	return got
}
