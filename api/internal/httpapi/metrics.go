package httpapi

import (
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the RED metrics (Rate, Errors, Duration) for the HTTP server.
//
// Every label value comes from a bounded set (known methods, route patterns, status codes):
// a label taken from user input, such as the raw URL path, would create one time series per
// distinct value and can exhaust Prometheus memory (a cardinality explosion).
type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by method, route pattern and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method and route pattern.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.inFlight)
	return m
}

// methodLabel maps arbitrary client-sent methods to a fixed set.
func methodLabel(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

// routeLabel turns the matched ServeMux pattern ("GET /api/v1/tasks/{id}") into "/api/v1/tasks/{id}".
// Requests that matched no pattern (404, 405) share a single "unmatched" series.
func routeLabel(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return path
	}
	return pattern
}
