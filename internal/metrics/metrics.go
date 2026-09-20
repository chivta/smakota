// Package metrics holds the one registry. Like logging it is a cross-cutting
// concern, reached through package-level functions rather than injection.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	// Errors is driven by the zerolog hook below, so an error-level log event
	// and its counter can never drift apart.
	Errors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "calbot_errors_total",
		Help: "Error-level log events, by the component that logged them.",
	}, []string{"component"})

	HTTPRequests = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "calbot_http_request_seconds",
		Help:    "HTTP request duration.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route", "status"})

	ResolverAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "calbot_resolver_attempts_total",
		Help: "Resolver passes, by input type and outcome.",
	}, []string{"input_type", "outcome"})

	ResolverLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "calbot_resolver_seconds",
		Help:    "Time from input to candidate list.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 15},
	}, []string{"input_type"})

	IngestRows = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "calbot_ingest_rows_total",
		Help: "Rows moved through the pipeline, by source and stage.",
	}, []string{"source", "stage"})

	// IngestAge answers "is the data fresh" and backs the staleness alert.
	IngestAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "calbot_ingest_last_success_seconds",
		Help: "Seconds since the last successful run, by source.",
	}, []string{"source"})
)

// errorHook increments Errors on every error-level event.
type errorHook struct{}

func (errorHook) Run(e *zerolog.Event, level zerolog.Level, _ string) {
	if level < zerolog.ErrorLevel {
		return
	}
	Errors.WithLabelValues("app").Inc()
}

// Init attaches the error counter to the global logger. Call it after
// logging.Init.
func Init() {
	log.Logger = log.Logger.Hook(errorHook{})
}
