// Package metrics implements the zealot Prometheus metric spec.
// Metric names and labels are part of SPEC v1 and must stay identical
// across future language ports.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Version is stamped into zealot_build_info; overridden at build time.
var Version = "dev"

// Metrics holds the spec-defined collectors for one service instance.
type Metrics struct {
	registry *prometheus.Registry

	requestsTotal  *prometheus.CounterVec
	requestDur     *prometheus.HistogramVec
	stepDur        *prometheus.HistogramVec
	upstreamTotal  *prometheus.CounterVec
	datastoreDur   *prometheus.HistogramVec

	service string
}

var latencyBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// New creates and registers all spec metrics for service.
func New(service string) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{registry: reg, service: service}

	m.requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "zealot_requests_total",
		Help: "Total HTTP requests handled.",
	}, []string{"service", "endpoint", "status"})

	m.requestDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zealot_request_duration_seconds",
		Help:    "Request latency per endpoint.",
		Buckets: latencyBuckets,
	}, []string{"service", "endpoint"})

	m.stepDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zealot_step_duration_seconds",
		Help:    "Pipeline step latency.",
		Buckets: latencyBuckets,
	}, []string{"service", "endpoint", "step_type", "target"})

	m.upstreamTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "zealot_upstream_requests_total",
		Help: "Total upstream calls made.",
	}, []string{"service", "target", "status"})

	m.datastoreDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zealot_datastore_duration_seconds",
		Help:    "Datastore query latency.",
		Buckets: latencyBuckets,
	}, []string{"service", "datastore", "op"})

	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zealot_build_info",
		Help: "Build information; value is always 1.",
	}, []string{"version", "lang"})
	build.WithLabelValues(Version, "go").Set(1)

	reg.MustRegister(m.requestsTotal, m.requestDur, m.stepDur,
		m.upstreamTotal, m.datastoreDur, build)
	return m
}

// Handler serves /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveRequest records one completed request.
func (m *Metrics) ObserveRequest(endpoint string, status int, d time.Duration) {
	m.requestsTotal.WithLabelValues(m.service, endpoint, strconv.Itoa(status)).Inc()
	m.requestDur.WithLabelValues(m.service, endpoint).Observe(d.Seconds())
}

// ObserveStep implements pipeline.Recorder.
func (m *Metrics) ObserveStep(endpoint, stepType, target string, d time.Duration) {
	m.stepDur.WithLabelValues(m.service, endpoint, stepType, target).Observe(d.Seconds())
}

// IncUpstream implements pipeline.Recorder.
func (m *Metrics) IncUpstream(target string, status int) {
	m.upstreamTotal.WithLabelValues(m.service, target, strconv.Itoa(status)).Inc()
}

// ObserveDatastore records one datastore query (used from M2).
func (m *Metrics) ObserveDatastore(datastore, op string, d time.Duration) {
	m.datastoreDur.WithLabelValues(m.service, datastore, op).Observe(d.Seconds())
}
