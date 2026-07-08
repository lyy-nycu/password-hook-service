package observability

import (
	"context"
	"sync"
	"time"
)

type Labels map[string]string

type Recorder interface {
	Inc(context.Context, string, Labels)
	ObserveDuration(context.Context, string, time.Duration, Labels)
	SetGauge(context.Context, string, int64, Labels)
}

type NoopRecorder struct{}

func (NoopRecorder) Inc(context.Context, string, Labels)                            {}
func (NoopRecorder) ObserveDuration(context.Context, string, time.Duration, Labels) {}
func (NoopRecorder) SetGauge(context.Context, string, int64, Labels)                {}

type Sample struct {
	Name     string
	Labels   Labels
	Duration time.Duration
	Value    int64
}

type CaptureRecorder struct {
	mu        sync.Mutex
	counters  map[string][]Sample
	durations map[string][]Sample
	gauges    map[string][]Sample
}

func NewCaptureRecorder() *CaptureRecorder {
	return &CaptureRecorder{
		counters:  make(map[string][]Sample),
		durations: make(map[string][]Sample),
		gauges:    make(map[string][]Sample),
	}
}

func (r *CaptureRecorder) Inc(_ context.Context, name string, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[name] = append(r.counters[name], Sample{Name: name, Labels: copyLabels(labels), Value: 1})
}

func (r *CaptureRecorder) ObserveDuration(_ context.Context, name string, duration time.Duration, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.durations[name] = append(r.durations[name], Sample{Name: name, Labels: copyLabels(labels), Duration: duration})
}

func (r *CaptureRecorder) SetGauge(_ context.Context, name string, value int64, labels Labels) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[name] = append(r.gauges[name], Sample{Name: name, Labels: copyLabels(labels), Value: value})
}

func (r *CaptureRecorder) Counters(name string) []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sample(nil), r.counters[name]...)
}

func (r *CaptureRecorder) Durations(name string) []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sample(nil), r.durations[name]...)
}

func (r *CaptureRecorder) Gauges(name string) []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Sample(nil), r.gauges[name]...)
}

func copyLabels(labels Labels) Labels {
	out := make(Labels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}
