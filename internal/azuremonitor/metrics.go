package azuremonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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
	name     string
	labels   observability.Labels
	min      float64
	max      float64
	sum      float64
	count    int64
	observed bool
}

func NewMetricRecorder(options MetricRecorderOptions) *MetricRecorder {
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	options.EndpointBaseURL = strings.TrimSpace(options.EndpointBaseURL)
	options.ResourceID = strings.TrimSpace(options.ResourceID)
	options.Region = strings.TrimSpace(options.Region)
	options.Namespace = strings.TrimSpace(options.Namespace)
	return &MetricRecorder{options: options, points: map[string]metricPoint{}}
}

func (r *MetricRecorder) Inc(_ context.Context, name string, labels observability.Labels) {
	r.add(name, labels, 1)
}

func (r *MetricRecorder) ObserveDuration(_ context.Context, name string, duration time.Duration, labels observability.Labels) {
	r.add(name, labels, duration.Seconds())
}

func (r *MetricRecorder) SetGauge(_ context.Context, name string, value int64, labels observability.Labels) {
	r.add(name, labels, float64(value))
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
	if r.options.TokenSource == nil {
		return errors.New("azure monitor token source is required")
	}
	token, err := r.options.TokenSource.Token(ctx)
	if err != nil {
		return fmt.Errorf("get azure monitor token: %w", err)
	}
	for _, payload := range r.azureMonitorPayloads(points) {
		if err := r.postPayload(ctx, token, payload); err != nil {
			return err
		}
	}
	return nil
}

func (r *MetricRecorder) add(name string, labels observability.Labels, value float64) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	labels = allowedLabels(labels)

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

func (r *MetricRecorder) postPayload(ctx context.Context, token string, payload azureMonitorPayload) error {
	body, err := json.Marshal(payload)
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

func (r *MetricRecorder) azureMonitorPayloads(points []metricPoint) []azureMonitorPayload {
	groups := map[string][]metricPoint{}
	for _, point := range points {
		dimNames := sortedLabelNames(point.labels)
		key := point.name + "\x00" + strings.Join(dimNames, "\x00")
		groups[key] = append(groups[key], point)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	payloads := make([]azureMonitorPayload, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool {
			return metricKey(group[i].name, group[i].labels) < metricKey(group[j].name, group[j].labels)
		})
		payloads = append(payloads, r.azureMonitorPayload(group))
	}
	return payloads
}

func (r *MetricRecorder) azureMonitorPayload(points []metricPoint) azureMonitorPayload {
	dimNames := sortedLabelNames(points[0].labels)
	series := make([]azureMonitorSeries, 0, len(points))
	for _, point := range points {
		dimValues := make([]string, 0, len(dimNames))
		for _, name := range dimNames {
			dimValues = append(dimValues, point.labels[name])
		}
		series = append(series, azureMonitorSeries{
			DimValues: dimValues,
			Min:       point.min,
			Max:       point.max,
			Sum:       point.sum,
			Count:     point.count,
		})
	}
	return azureMonitorPayload{
		Time: r.options.Now().UTC().Format(time.RFC3339),
		Data: azureMonitorData{BaseData: azureMonitorBaseData{
			Metric:    points[0].name,
			Namespace: r.options.Namespace,
			DimNames:  dimNames,
			Series:    series,
		}},
	}
}

type azureMonitorPayload struct {
	Time string           `json:"time"`
	Data azureMonitorData `json:"data"`
}

type azureMonitorData struct {
	BaseData azureMonitorBaseData `json:"baseData"`
}

type azureMonitorBaseData struct {
	Metric    string               `json:"metric"`
	Namespace string               `json:"namespace"`
	DimNames  []string             `json:"dimNames,omitempty"`
	Series    []azureMonitorSeries `json:"series"`
}

type azureMonitorSeries struct {
	DimValues []string `json:"dimValues,omitempty"`
	Min       float64  `json:"min"`
	Max       float64  `json:"max"`
	Sum       float64  `json:"sum"`
	Count     int64    `json:"count"`
}

type credentialTokenSource struct {
	credential azcore.TokenCredential
	scope      string
}

func NewCredentialTokenSource(credential azcore.TokenCredential, scope string) TokenSource {
	return credentialTokenSource{credential: credential, scope: strings.TrimSpace(scope)}
}

func (s credentialTokenSource) Token(ctx context.Context) (string, error) {
	if s.credential == nil {
		return "", errors.New("azure credential is required")
	}
	token, err := s.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{s.scope}})
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

func metricKey(name string, labels observability.Labels) string {
	keys := sortedLabelNames(labels)
	parts := make([]string, 0, 1+len(keys)*2)
	parts = append(parts, name)
	for _, key := range keys {
		parts = append(parts, key, labels[key])
	}
	return strings.Join(parts, "\x00")
}

func allowedLabels(labels observability.Labels) observability.Labels {
	if len(labels) == 0 {
		return nil
	}
	allowed := map[string]struct{}{
		"attempts":     {},
		"eventType":    {},
		"identityType": {},
		"kind":         {},
		"middleware":   {},
		"outcome":      {},
		"queue":        {},
		"reason":       {},
		"status":       {},
	}
	out := make(observability.Labels)
	for key, value := range labels {
		if _, ok := allowed[key]; !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[key] = value
		if len(out) == 9 {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedLabelNames(labels observability.Labels) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyLabels(labels observability.Labels) observability.Labels {
	if len(labels) == 0 {
		return nil
	}
	out := make(observability.Labels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}
