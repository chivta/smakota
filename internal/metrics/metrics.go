// Package metrics holds the one registry. Like logging it is a cross-cutting
// concern, reached through package-level functions rather than injection.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/push"
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
		Help: "Rows moved through the pipeline in the most recent run of the source, by source and stage. Each run is a fresh process pushed to the Pushgateway, so this is a per-run count that resets, not a lifetime total: do not use rate().",
	}, []string{"source", "stage"})

	// IngestAge answers "is the data fresh" and backs the staleness alert.
	IngestAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "calbot_ingest_last_success_seconds",
		Help: "Seconds since the last successful run, by source.",
	}, []string{"source"})

	// Entries answers whether people find their food in the catalogue or type
	// the numbers by hand, and which source earns its keep.
	Entries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "calbot_entries_total",
		Help: "Log entries saved, by path, amount kind and food source.",
	}, []string{"path", "amount", "food_source"})

	// SearchResults separates a catalogue gap (0) from a ranking problem (100).
	SearchResults = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "calbot_search_results",
		Help:    "Candidates returned per search.",
		Buckets: []float64{0, 1, 2, 3, 5, 10, 20, 50, 100},
	})

	PersonalFoods = promauto.NewCounter(prometheus.CounterOpts{
		Name: "calbot_personal_foods_total",
		Help: "Personal foods created.",
	})

	// CatalogueFoods and ActiveUsers come from aggregate SQL, so a ticker
	// refreshes them instead of every scrape.
	CatalogueFoods = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "calbot_catalogue_foods",
		Help: "Catalogue rows, by source.",
	}, []string{"source"})

	ActiveUsers = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "calbot_active_users",
		Help: "Distinct users with a log entry created inside the window.",
	}, []string{"window"})
)

// ingestPushJob and ingestPushSourceLabel identify one source's group in the
// Pushgateway, so the CronJobs do not overwrite each other.
const (
	ingestPushJob         = "calbot_ingest"
	ingestPushSourceLabel = "ingest_source"
)

// PushIngest sends the default registry to the Pushgateway, grouped by source.
// A CronJob exits before Prometheus could scrape it.
func PushIngest(url, source string) error {
	return push.New(url, ingestPushJob).
		Grouping(ingestPushSourceLabel, source).
		Gatherer(prometheus.DefaultGatherer).
		Push()
}

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
