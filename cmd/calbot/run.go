package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/api"
	"github.com/arvlas/calbot/internal/config"
	"github.com/arvlas/calbot/internal/ingest"
	"github.com/arvlas/calbot/internal/ingest/fora"
	"github.com/arvlas/calbot/internal/ingest/mcdonalds"
	"github.com/arvlas/calbot/internal/ingest/off"
	"github.com/arvlas/calbot/internal/ingest/proe"
	"github.com/arvlas/calbot/internal/ingest/silpo"
	"github.com/arvlas/calbot/internal/ingest/usda"
	"github.com/arvlas/calbot/internal/ingest/zakaz"
	"github.com/arvlas/calbot/internal/metrics"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/arvlas/calbot/internal/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// ingestAllName is the `ingest` argument and manual job that run every adapter.
const ingestAllName = "all"

// ingestAgeRefresh is how often the freshness gauge is recomputed.
const ingestAgeRefresh = time.Minute

// productStatsRefresh is how often the aggregate-SQL gauges are recomputed;
// counting every food row per scrape would be too heavy.
const productStatsRefresh = 5 * time.Minute

// Labels of metrics.ActiveUsers.
const (
	windowDay   = "1d"
	windowWeek  = "7d"
	windowMonth = "30d"
)

// shutdownGrace is how long in-flight requests get to finish after a signal.
const shutdownGrace = 15 * time.Second

// container holds the dependency graph. The whole of it is visible here, in one
// place, without tooling.
type container struct {
	cfg      config.Config
	pool     *pgxpool.Pool
	users    *postgres.UserRepo
	tokens   *postgres.TokenRepo
	foods    *postgres.FoodRepo
	logs     *postgres.LogRepo
	reviews  *postgres.ReviewRepo
	ingests  *postgres.IngestRepo
	locker   *postgres.Locker
	registry *ingest.Registry
	runner   *ingest.Runner
}

// build connects to the database, runs migrations, and wires every component.
func build(ctx context.Context, cfg config.Config) (*container, error) {
	err := postgres.Migrate(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}

	foods := postgres.NewFoodRepo(pool, resolver.Normalize)
	ingests := postgres.NewIngestRepo(pool, resolver.Normalize)

	httpClient := ingest.NewHTTPClient(nil, cfg.IngestUserAgent)
	// The dump download outlasts the default client's whole-request timeout.
	dumpClient := ingest.NewHTTPClient(&http.Client{}, cfg.IngestUserAgent)
	registry, err := ingest.NewRegistry(
		silpo.New(httpClient, cfg.SilpoBranchID),
		zakaz.New(zakaz.NewHTTPClient(cfg.IngestUserAgent), cfg.ZakazStoreIDs),
		fora.New(httpClient, cfg.ForaFilialIDs),
		proe.New(httpClient, cfg.ProeBaseURL),
		usda.New(httpClient, cfg.USDAAPIKey, cfg.USDAQueries),
		mcdonalds.New(httpClient, cfg.McDonaldsPDFURL),
	)
	if err == nil {
		err = registry.Register(offDeltaJob, off.NewDelta(httpClient))
	}
	if err == nil {
		err = registry.Register(offFullJob, off.NewFull(dumpClient, cfg.OFFDumpURL))
	}
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("build ingest registry: %w", err)
	}

	return &container{
		cfg:      cfg,
		pool:     pool,
		users:    postgres.NewUserRepo(pool),
		tokens:   postgres.NewTokenRepo(pool),
		foods:    foods,
		logs:     postgres.NewLogRepo(pool),
		reviews:  postgres.NewReviewRepo(pool),
		ingests:  ingests,
		locker:   postgres.NewLocker(pool),
		registry: registry,
		runner:   ingest.NewRunner(ingestStore{repo: ingests}, cfg.IngestRejectCap),
	}, nil
}

func (c *container) close() {
	c.pool.Close()
}

func runMigrate(ctx context.Context, cfg config.Config) error {
	return postgres.Migrate(ctx, cfg.DatabaseURL)
}

func runAPI(ctx context.Context, cfg config.Config) error {
	c, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.close()

	server := api.New(api.Deps{
		Users:   c.users,
		Tokens:  c.tokens,
		Foods:   c.foods,
		Logs:    c.logs,
		Reviews: c.reviews,
	})

	go c.trackIngestAge(ctx)
	go c.trackProductStats(ctx)

	httpServer := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: server.Router(cfg.IsProduction()),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()

		shutdownErr := httpServer.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			log.Error().Err(shutdownErr).Msg("http shutdown failed")
		}
	}()

	log.Info().Str("addr", cfg.HTTPAddr).Msg("api listening")

	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// trackIngestAge keeps metrics.IngestAge at the real seconds since each source
// last succeeded. It runs in the api process because that is the one serving
// /metrics. A source that never succeeded has no sample.
func (c *container) trackIngestAge(ctx context.Context) {
	ticker := time.NewTicker(ingestAgeRefresh)
	defer ticker.Stop()

	for {
		for _, adapter := range c.registry.Adapters() {
			source := adapter.Name()
			finishedAt, ok, err := c.ingests.LastSuccess(ctx, source)
			if err != nil {
				log.Error().Err(err).Str("source", source).Msg("read last ingest success")
				continue
			}
			if ok {
				metrics.IngestAge.WithLabelValues(source).Set(time.Since(finishedAt).Seconds())
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// trackProductStats keeps the catalogue-size and active-user gauges fresh. A
// failed refresh is logged and the previous values stay.
func (c *container) trackProductStats(ctx context.Context) {
	ticker := time.NewTicker(productStatsRefresh)
	defer ticker.Stop()

	for {
		counts, err := c.foods.CountBySource(ctx)
		if err != nil {
			log.Error().Err(err).Msg("refresh catalogue size")
		} else {
			metrics.CatalogueFoods.Reset()
			for source, n := range counts {
				metrics.CatalogueFoods.WithLabelValues(source).Set(float64(n))
			}
		}

		active, err := c.logs.ActiveUsers(ctx)
		if err != nil {
			log.Error().Err(err).Msg("refresh active users")
		} else {
			metrics.ActiveUsers.WithLabelValues(windowDay).Set(float64(active.Day))
			metrics.ActiveUsers.WithLabelValues(windowWeek).Set(float64(active.Week))
			metrics.ActiveUsers.WithLabelValues(windowMonth).Set(float64(active.Month))
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runIngest(ctx context.Context, cfg config.Config, name string) error {
	c, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.close()

	runErr := c.ingestSource(ctx, name)

	// Pushed even after a failed run: its row counts are what debugging needs.
	if cfg.PushgatewayURL != "" {
		pushErr := metrics.PushIngest(cfg.PushgatewayURL, name)
		if pushErr != nil {
			log.Error().Err(pushErr).Msg("push ingest metrics")
		}
	}
	return runErr
}

func (c *container) ingestSource(ctx context.Context, name string) error {
	if name == ingestAllName {
		return c.ingestAll(ctx)
	}

	adapter, err := c.registry.Resolve(name)
	if err != nil {
		return err
	}

	log.Info().Str("source", name).Msg("ingest starting")
	return c.runner.Run(ctx, adapter)
}

// ingestAll runs every adapter and fails only when none of them succeeded, so
// one dead provider does not break the deploy's smoke test.
func (c *container) ingestAll(ctx context.Context) error {
	results := c.runner.RunAll(ctx, c.registry.Adapters())

	var failures []error
	for _, result := range results {
		if result.Err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", result.Source, result.Err))
		}
	}
	if len(results) > 0 && len(failures) == len(results) {
		return fmt.Errorf("every ingest source failed: %w", errors.Join(failures...))
	}
	return ctx.Err()
}
