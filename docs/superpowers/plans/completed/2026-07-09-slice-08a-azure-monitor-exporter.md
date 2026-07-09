# Slice 8A Azure Monitor Exporter Implementation Plan

> **Plan Status:** Completed
>
> **Source Refresh:** Refreshed on 2026-07-09 against current Microsoft Learn pages for Azure Container Apps managed OpenTelemetry agents, Azure Monitor OpenTelemetry distro support, and Azure Monitor custom metrics REST API.
>
> **Completion Note:** Implemented in PR #11 through Azure Monitor config, custom metrics publication, OpenTelemetry lifecycle wiring, documentation, and review feedback fixes.
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make password-hook-service observability visible in Azure Monitor: structured logs, traces, and custom metrics.

**Architecture:** Keep Slice 8's application-owned observability boundary. Export logs/traces through OpenTelemetry OTLP so Azure Container Apps can forward them to Azure Monitor Application Insights, while exporting custom metrics through Azure Monitor's custom metrics REST API because the Container Apps managed OpenTelemetry Application Insights destination currently supports logs and traces but not metrics. Metrics use managed identity with the `Monitoring Metrics Publisher` role and are preaggregated before publication.

**Tech Stack:** Go OpenTelemetry SDK, OTLP exporter, `log/slog`, Azure Container Apps managed OpenTelemetry agent, Azure Monitor Application Insights, Azure Monitor custom metrics REST API, Azure Identity `DefaultAzureCredential`, unit tests with `httptest`.

---

## Source Notes

- Microsoft Learn's Azure Monitor OpenTelemetry distro page is currently titled for .NET, Node.js, Python, and Java applications, not Go. For Go, use the upstream OpenTelemetry Go SDK with OTLP exporters rather than assuming a Microsoft Go distro exists.
- Microsoft Learn's Azure Container Apps managed OpenTelemetry agent page says the Application Insights destination supports logs and traces, but not metrics. This plan keeps logs/traces on OTLP and handles metrics separately.
- Microsoft Learn's Azure Monitor custom metrics REST API page supports custom metrics for Azure resources using Microsoft Entra auth, including managed identities with `Monitoring Metrics Publisher`.
- Microsoft Learn's Azure Monitor custom metrics overview currently labels classic custom metrics as preview/non-GA and points to Azure Monitor Workspace custom metrics as the improved GA direction. This slice still implements the documented REST API behind an isolated `internal/azuremonitor` adapter so the publication path can be swapped later without changing Slice 8's application-owned recorder boundary.

## Scope And Constraints

- Do not put Application Insights connection strings, authorization tokens, Graph secrets, HMAC secrets, password encryption keys, request bodies, queue bodies, or passwords in logs or metric labels.
- Do not replace Slice 8's recorder/test architecture. This slice supplies production exporters behind that boundary.
- Do not require portal changes.
- Use managed identity for Azure Monitor custom metrics. Do not introduce a client secret for metrics publishing.
- Logs/traces and metrics may have different Azure Monitor destinations: Application Insights for logs/traces, Azure Monitor Metrics for custom metrics.
- Keep metric dimensions low-cardinality and within Azure Monitor custom metric limits; never include CN, UPN, trace IDs, request IDs, message IDs, nonces, signatures, ciphertext, or password-derived values as metric dimensions.

## File Structure

- Modify `internal/config/config.go`: add exporter mode and Azure Monitor settings.
- Modify `internal/config/config_test.go`: cover exporter config loading and validation.
- Create `internal/azuremonitor/metrics.go`: Azure Monitor custom metrics recorder.
- Create `internal/azuremonitor/metrics_test.go`: request-shape, labels, aggregation, auth, and no-secret tests.
- Create `internal/azuremonitor/otel.go`: OpenTelemetry log/trace setup and shutdown.
- Create `internal/azuremonitor/otel_test.go`: config validation and no-op behavior tests.
- Modify `internal/app/app.go`: wire the Azure Monitor recorder and OpenTelemetry lifecycle when enabled.
- Modify `internal/app/app_test.go`: assert no-op default and Azure Monitor mode wiring.
- Modify `README.md`: document Azure Monitor logs, traces, metrics, environment variables, RBAC, and verification queries.
- Modify `docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md`: record the ACA OpenTelemetry agent and `Monitoring Metrics Publisher` infrastructure requirements for the later infrastructure slice.

---

### Task 1: Azure Monitor Exporter Configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add to `internal/config/config_test.go`:

```go
func TestLoadAzureMonitorExporterConfig(t *testing.T) {
	t.Setenv("OBSERVABILITY_EXPORTER", " azure_monitor ")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("AZURE_MONITOR_METRIC_RESOURCE_ID", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/password-hook")
	t.Setenv("AZURE_MONITOR_METRIC_REGION", "eastasia")
	t.Setenv("AZURE_MONITOR_METRIC_NAMESPACE", "password-hook-service")

	cfg := Load()

	if cfg.ObservabilityExporter != ObservabilityExporterAzureMonitor {
		t.Fatalf("ObservabilityExporter = %q, want azure_monitor", cfg.ObservabilityExporter)
	}
	if cfg.OTLPExporterEndpoint != "http://localhost:4318" {
		t.Fatalf("OTLPExporterEndpoint = %q", cfg.OTLPExporterEndpoint)
	}
	if cfg.AzureMonitorMetricResourceID == "" || cfg.AzureMonitorMetricRegion != "eastasia" || cfg.AzureMonitorMetricNamespace != "password-hook-service" {
		t.Fatalf("Azure Monitor metric config = %#v", cfg)
	}
}

func TestValidateAzureMonitorExporterRequiresMetricConfig(t *testing.T) {
	t.Parallel()

	cfg := completeConfig()
	cfg.ObservabilityExporter = ObservabilityExporterAzureMonitor
	cfg.OTLPExporterEndpoint = "http://localhost:4318"
	cfg.AzureMonitorMetricResourceID = ""
	cfg.AzureMonitorMetricRegion = "eastasia"
	cfg.AzureMonitorMetricNamespace = "password-hook-service"

	err := cfg.Validate()
	if err == nil || err.Error() != "AZURE_MONITOR_METRIC_RESOURCE_ID is required when OBSERVABILITY_EXPORTER=azure_monitor" {
		t.Fatalf("Validate error = %v", err)
	}
}
```

- [ ] **Step 2: Run config tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/config`

Expected: FAIL because observability exporter config does not exist.

- [ ] **Step 3: Add config fields and validation**

In `internal/config/config.go`, add:

```go
const (
	ObservabilityExporterNone         = "none"
	ObservabilityExporterAzureMonitor = "azure_monitor"
)
```

Add fields to `Config`:

```go
ObservabilityExporter        string
OTLPExporterEndpoint         string
AzureMonitorMetricResourceID string
AzureMonitorMetricRegion     string
AzureMonitorMetricNamespace  string
```

Load them:

```go
ObservabilityExporter:        env("OBSERVABILITY_EXPORTER", ObservabilityExporterNone),
OTLPExporterEndpoint:         strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
AzureMonitorMetricResourceID: strings.TrimSpace(os.Getenv("AZURE_MONITOR_METRIC_RESOURCE_ID")),
AzureMonitorMetricRegion:     strings.TrimSpace(os.Getenv("AZURE_MONITOR_METRIC_REGION")),
AzureMonitorMetricNamespace:  env("AZURE_MONITOR_METRIC_NAMESPACE", "password-hook-service"),
```

Call `validateObservability()` from `Validate()`:

```go
func (c Config) validateObservability() error {
	switch c.ObservabilityExporter {
	case "", ObservabilityExporterNone:
		return nil
	case ObservabilityExporterAzureMonitor:
		if strings.TrimSpace(c.OTLPExporterEndpoint) == "" {
			return errors.New("OTEL_EXPORTER_OTLP_ENDPOINT is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		if strings.TrimSpace(c.AzureMonitorMetricResourceID) == "" {
			return errors.New("AZURE_MONITOR_METRIC_RESOURCE_ID is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		if strings.TrimSpace(c.AzureMonitorMetricRegion) == "" {
			return errors.New("AZURE_MONITOR_METRIC_REGION is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		if strings.TrimSpace(c.AzureMonitorMetricNamespace) == "" {
			return errors.New("AZURE_MONITOR_METRIC_NAMESPACE is required when OBSERVABILITY_EXPORTER=azure_monitor")
		}
		return nil
	default:
		return errors.New("OBSERVABILITY_EXPORTER must be none or azure_monitor")
	}
}
```

- [ ] **Step 4: Run focused tests**

Run: `/usr/local/go/bin/go test ./internal/config`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add azure monitor observability config"
```

### Task 2: Azure Monitor Custom Metrics Recorder

**Files:**
- Create: `internal/azuremonitor/metrics.go`
- Create: `internal/azuremonitor/metrics_test.go`

- [ ] **Step 1: Write failing custom metrics tests**

Create `internal/azuremonitor/metrics_test.go`:

```go
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
		"status": "202",
		"outcome": "enqueued",
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
	for _, forbidden := range []string{"password", "passwordCiphertext", "passwordNonce", "Authorization"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("custom metric body leaked forbidden value %q: %s", forbidden, body)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/azuremonitor`

Expected: FAIL because `internal/azuremonitor` does not exist.

- [ ] **Step 3: Implement custom metrics recorder**

Create `internal/azuremonitor/metrics.go` with:

```go
package azuremonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nycu/password-hook-service/internal/observability"
)

type TokenSource interface {
	Token(context.Context) (string, error)
}

type MetricRecorderOptions struct {
	EndpointBaseURL string
	ResourceID      string
	Region          string
	Namespace       string
	TokenSource     TokenSource
	HTTPClient      *http.Client
	Now             func() time.Time
}

type MetricRecorder struct {
	mu      sync.Mutex
	options MetricRecorderOptions
	points  map[string]metricPoint
}

type metricPoint struct {
	name      string
	labels    observability.Labels
	min       float64
	max       float64
	sum       float64
	count     int64
	observed  bool
}

func NewMetricRecorder(options MetricRecorderOptions) *MetricRecorder {
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &MetricRecorder{options: options, points: map[string]metricPoint{}}
}

func (r *MetricRecorder) Inc(ctx context.Context, name string, labels observability.Labels) {
	r.add(name, labels, 1)
}

func (r *MetricRecorder) ObserveDuration(ctx context.Context, name string, duration time.Duration, labels observability.Labels) {
	r.add(name, labels, duration.Seconds())
}

func (r *MetricRecorder) SetGauge(ctx context.Context, name string, value int64, labels observability.Labels) {
	r.add(name, labels, float64(value))
}

func (r *MetricRecorder) add(name string, labels observability.Labels, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := metricKey(name, labels)
	point := r.points[key]
	if !point.observed {
		point = metricPoint{name: name, labels: copyLabels(labels), min: value, max: value, observed: true}
	}
	if value < point.min {
		point.min = value
	}
	if value > point.max {
		point.max = value
	}
	point.sum += value
	point.count++
	r.points[key] = point
}

func (r *MetricRecorder) Flush(ctx context.Context) error {
	r.mu.Lock()
	points := make([]metricPoint, 0, len(r.points))
	for _, point := range r.points {
		points = append(points, point)
	}
	r.points = map[string]metricPoint{}
	r.mu.Unlock()
	if len(points) == 0 {
		return nil
	}
	token, err := r.options.TokenSource.Token(ctx)
	if err != nil {
		return fmt.Errorf("get azure monitor token: %w", err)
	}
	body, err := json.Marshal(r.azureMonitorPayload(points))
	if err != nil {
		return fmt.Errorf("marshal azure monitor metrics: %w", err)
	}
	endpoint := strings.TrimRight(r.options.EndpointBaseURL, "/") + "/" + url.PathEscape(r.options.ResourceID) + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.options.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send azure monitor metrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("send azure monitor metrics: status %d", resp.StatusCode)
	}
	return nil
}
```

Complete helper functions to produce Azure Monitor's `baseData.metric`, `namespace`, `dimNames`, and `series` shape. Keep dimension count under 10; drop labels not explicitly allowlisted by Slice 8.

- [ ] **Step 4: Run focused tests**

Run: `/usr/local/go/bin/go test ./internal/azuremonitor`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/azuremonitor/metrics.go internal/azuremonitor/metrics_test.go
git commit -m "feat: publish observability metrics to azure monitor"
```

### Task 3: OpenTelemetry Logs And Traces Export

**Files:**
- Create: `internal/azuremonitor/otel.go`
- Create: `internal/azuremonitor/otel_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write failing OTel setup tests**

Create `internal/azuremonitor/otel_test.go` with tests that assert empty endpoint returns no-op setup, and a non-empty endpoint returns a shutdown function without exporting password fields.

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/azuremonitor`

Expected: FAIL because OTel setup does not exist.

- [ ] **Step 3: Implement OTel setup**

Create `internal/azuremonitor/otel.go`:

```go
package azuremonitor

import (
	"context"
	"errors"
	"strings"
)

type OTelOptions struct {
	ServiceName  string
	OTLPEndpoint string
}

type ShutdownFunc func(context.Context) error

func SetupOTel(ctx context.Context, options OTelOptions) (ShutdownFunc, error) {
	if strings.TrimSpace(options.OTLPEndpoint) == "" {
		return func(context.Context) error { return nil }, nil
	}
	if strings.TrimSpace(options.ServiceName) == "" {
		return nil, errors.New("otel service name is required")
	}
	// Implement with OpenTelemetry Go SDK OTLP exporters using current
	// package versions. Configure service.name=password-hook-service,
	// HTTP server spans, worker spans, and Graph processor spans.
	return func(context.Context) error { return nil }, nil
}
```

Complete `SetupOTel` in the same step with real OpenTelemetry SDK setup:

- resource `service.name=password-hook-service`
- OTLP exporter endpoint from `OTEL_EXPORTER_OTLP_ENDPOINT`
- tracer provider shutdown registered with app closers
- slog bridge or OTel log provider only if it does not duplicate sensitive payloads

- [ ] **Step 4: Wire OTel lifecycle into app**

In `internal/app/app.go`, when `cfg.ObservabilityExporter == config.ObservabilityExporterAzureMonitor`, call `azuremonitor.SetupOTel` and append its shutdown to app closers.

- [ ] **Step 5: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/azuremonitor ./internal/app
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/azuremonitor/otel.go internal/azuremonitor/otel_test.go internal/app/app.go
git commit -m "feat: export traces and logs through opentelemetry"
```

### Task 4: App Wiring And Azure Monitor Documentation

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `README.md`
- Modify: `docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md`

- [ ] **Step 1: Write failing app wiring test**

Add an app test asserting `OBSERVABILITY_EXPORTER=azure_monitor` wires an Azure Monitor recorder instead of `observability.NoopRecorder{}` and registers OTel shutdown.

- [ ] **Step 2: Run tests and verify they fail**

Run: `/usr/local/go/bin/go test ./internal/app`

Expected: FAIL until app wiring is implemented.

- [ ] **Step 3: Wire Azure Monitor mode**

In `internal/app/app.go`, construct:

```go
recorder := observability.Recorder(observability.NoopRecorder{})
if cfg.ObservabilityExporter == config.ObservabilityExporterAzureMonitor {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	recorder = azuremonitor.NewMetricRecorder(azuremonitor.MetricRecorderOptions{
		EndpointBaseURL: "https://" + cfg.AzureMonitorMetricRegion + ".monitoring.azure.com",
		ResourceID:      cfg.AzureMonitorMetricResourceID,
		Region:          cfg.AzureMonitorMetricRegion,
		Namespace:       cfg.AzureMonitorMetricNamespace,
		TokenSource:     azuremonitor.NewCredentialTokenSource(credential, "https://monitoring.azure.com/.default"),
	})
}
```

Pass this recorder through hook, middleware, worker, Graph, and queue-depth wiring from Slice 8. Add a periodic flush loop or close-time flush so metrics reach Azure Monitor.

- [ ] **Step 4: Document Azure Monitor setup**

In `README.md`, add:

```markdown
## Azure Monitor Export

Set `OBSERVABILITY_EXPORTER=azure_monitor` to export production telemetry.

Logs and traces:
- Configure Azure Container Apps managed OpenTelemetry agent to send logs/traces to Application Insights.
- Set `OTEL_EXPORTER_OTLP_ENDPOINT` to the agent endpoint.

Metrics:
- Set `AZURE_MONITOR_METRIC_RESOURCE_ID` to the Azure resource ID that owns custom metrics.
- Set `AZURE_MONITOR_METRIC_REGION` to the resource region.
- Assign the runtime managed identity the `Monitoring Metrics Publisher` role for that resource.

Azure Container Apps Application Insights OpenTelemetry destination does not currently export metrics; this service publishes custom metrics through Azure Monitor's custom metrics REST API.
```

In `docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md`, add a Slice 8A refresh note to the Infrastructure Story:

```markdown
- Slice 8A requires the Container Apps environment to enable the managed OpenTelemetry agent for logs/traces to Application Insights, and requires the runtime managed identity to have `Monitoring Metrics Publisher` on the Azure resource used for custom metrics. Metrics are published by the application through the Azure Monitor custom metrics REST API because the ACA Application Insights OpenTelemetry destination does not currently accept metrics.
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
/usr/local/go/bin/go test ./internal/azuremonitor ./internal/app ./internal/config
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/app.go internal/app/app_test.go README.md docs/superpowers/plans/drafts/2026-07-03-slice-10-infrastructure.md
git commit -m "feat: wire azure monitor observability exporter"
```

### Task 5: Verification And Azure Smoke Test

**Files:**
- No source files unless verification exposes a defect.

- [ ] **Step 1: Run local verification**

Run:

```bash
/usr/local/go/bin/go test ./...
/usr/local/go/bin/go vet ./...
```

Expected: PASS.

- [ ] **Step 2: Run leak-focused scans**

Run:

```bash
rg -n "APPLICATIONINSIGHTS_CONNECTION_STRING|Authorization|Bearer|passwordCiphertext|passwordNonce|passwordKeyId|cleartext-password" internal README.md
```

Expected: code should not log or label secrets. README may mention environment variable names and forbidden fields.

- [ ] **Step 3: Run Azure smoke test in staging**

Deploy with:

```bash
OBSERVABILITY_EXPORTER=azure_monitor
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
AZURE_MONITOR_METRIC_RESOURCE_ID=/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-password-hook-staging/providers/Microsoft.App/containerApps/password-hook-staging
AZURE_MONITOR_METRIC_REGION=eastasia
AZURE_MONITOR_METRIC_NAMESPACE=password-hook-service
```

Expected:

- Application Insights shows request traces for hook calls.
- Application Insights or Log Analytics shows structured logs with `traceId`.
- Azure Monitor Metrics shows namespace `password-hook-service` with `hook_requests_total`, `middleware_requests_total`, `worker_messages_total`, `graph_upsert_duration_seconds`, and `queue_depth`.

- [ ] **Step 4: Commit verification fixes if needed**

```bash
git add internal/config/config.go internal/azuremonitor/metrics.go internal/azuremonitor/otel.go internal/app/app.go README.md
git commit -m "fix: address azure monitor exporter verification issues"
```
