package api

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds this service's Prometheus metrics, using a private registry
// per go-trust's convention (spec Appendix C item 1). Series chosen per
// spec §4.6's observability list: manifest requests by status, artifact
// requests by hash/status, bytes served, catalog entry count, build info —
// deliberately no per-client identifiers (spec §8.6).
type Metrics struct {
	registry *prometheus.Registry

	APIRequestsTotal    *prometheus.CounterVec
	APIRequestDuration  *prometheus.HistogramVec
	APIRequestsInFlight prometheus.Gauge

	ManifestRequestsTotal *prometheus.CounterVec // label: status
	ArtifactRequestsTotal *prometheus.CounterVec // labels: hash, status
	BytesServedTotal      prometheus.Counter
	CatalogEntryCount     prometheus.Gauge
	BuildInfo             *prometheus.GaugeVec // label: version
}

// NewMetrics creates and registers all metrics.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,

		APIRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "zkc_api_requests_total", Help: "Total number of API requests"},
			[]string{"method", "endpoint", "status"},
		),
		APIRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "zkc_api_request_duration_seconds",
				Help:    "Duration of API requests in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"method", "endpoint"},
		),
		APIRequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "zkc_api_requests_in_flight", Help: "Current number of API requests being processed",
		}),
		ManifestRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "zkc_manifest_requests_total", Help: "Total manifest.json requests by response status"},
			[]string{"status"},
		),
		ArtifactRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "zkc_artifact_requests_total", Help: "Total artifact download requests by hash and response status"},
			[]string{"hash", "status"},
		),
		BytesServedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "zkc_bytes_served_total", Help: "Total artifact bytes served",
		}),
		CatalogEntryCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "zkc_catalog_entry_count", Help: "Current number of entries in the loaded catalog",
		}),
		BuildInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "zkc_build_info", Help: "Build information, value is always 1"},
			[]string{"version"},
		),
	}
	registry.MustRegister(
		m.APIRequestsTotal, m.APIRequestDuration, m.APIRequestsInFlight,
		m.ManifestRequestsTotal, m.ArtifactRequestsTotal, m.BytesServedTotal,
		m.CatalogEntryCount, m.BuildInfo,
	)
	return m
}

// MetricsMiddleware records generic per-request metrics for every route except /metrics itself.
func (m *Metrics) MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}
		start := time.Now()
		m.APIRequestsInFlight.Inc()
		c.Next()
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		endpoint := c.FullPath()
		if endpoint == "" {
			endpoint = "unknown"
		}
		m.APIRequestsTotal.WithLabelValues(c.Request.Method, endpoint, status).Inc()
		m.APIRequestDuration.WithLabelValues(c.Request.Method, endpoint).Observe(duration)
		m.APIRequestsInFlight.Dec()
	}
}

// RegisterMetricsEndpoint wires the metrics middleware globally and exposes /metrics.
func RegisterMetricsEndpoint(r gin.IRouter, metrics *Metrics) {
	r.Use(metrics.MetricsMiddleware())
	r.GET("/metrics", gin.WrapH(promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})))
}
