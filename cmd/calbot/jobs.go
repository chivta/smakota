package main

import (
	"context"
	"fmt"
	"time"

	"github.com/arvlas/calbot/internal/config"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/arvlas/calbot/internal/scheduler"
	"github.com/arvlas/calbot/internal/seed"
	"github.com/rs/zerolog/log"
)

// Per-source run budgets. The OFF full dump is the long pole.
const (
	crawlTimeout    = 2 * time.Hour
	dumpTimeout     = 6 * time.Hour
	quickJobTimeout = 5 * time.Minute
	// allIngestTimeout covers every adapter running back to back.
	allIngestTimeout = dumpTimeout + 4*crawlTimeout
)

func runScheduler(ctx context.Context, cfg config.Config) error {
	c, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.close()

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return fmt.Errorf("load timezone %s: %w", cfg.Timezone, err)
	}

	scheduler.New(loc, c.locker, c.jobs()).Run(ctx)
	return nil
}

// jobs is the schedule from the PRD. Catalogue crawls are staggered across
// weekdays so one source's rate limit cannot starve another.
func (c *container) jobs() []scheduler.Job {
	ingestJob := func(name string, due func(time.Time) bool, timeout time.Duration) scheduler.Job {
		return scheduler.Job{
			Name:    name,
			Due:     due,
			Timeout: timeout,
			Run: func(ctx context.Context) error {
				adapter, err := c.registry.Resolve(name)
				if err != nil {
					return err
				}
				return c.runner.Run(ctx, adapter)
			},
		}
	}

	return []scheduler.Job{
		ingestJob("off-delta", scheduler.Daily(3, 0), crawlTimeout),
		ingestJob("off-full", scheduler.Weekly(time.Sunday, 2, 0), dumpTimeout),
		ingestJob("silpo", scheduler.Weekly(time.Monday, 2, 0), crawlTimeout),
		ingestJob("zakaz", scheduler.Weekly(time.Tuesday, 2, 0), crawlTimeout),
		ingestJob("proe", scheduler.Weekly(time.Wednesday, 2, 0), crawlTimeout),
		ingestJob("usda", scheduler.Weekly(time.Thursday, 2, 0), crawlTimeout),
		{
			Name:    "ingest-all",
			Due:     scheduler.Never(),
			Timeout: allIngestTimeout,
			Run:     c.ingestAll,
		},
		{
			Name:    "review-digest",
			Due:     scheduler.Daily(6, 0),
			Timeout: quickJobTimeout,
			Run:     c.reviewDigest,
		},
	}
}

// reviewDigest collapses the open review queue into one log line per run. The
// queue itself is what drives catalogue expansion.
func (c *container) reviewDigest(ctx context.Context) error {
	items, err := c.reviews.ListOpen(ctx, 100)
	if err != nil {
		return fmt.Errorf("list open review items: %w", err)
	}

	log.Info().Int("open", len(items)).Msg("review queue digest")
	return nil
}

// runSeed loads the curated catalogue. It is re-runnable: entries are keyed by
// source and external ID, so a second run updates rather than duplicates.
func runSeed(ctx context.Context, cfg config.Config) error {
	c, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.close()

	foods, err := seed.Load()
	if err != nil {
		return fmt.Errorf("load curated catalogue: %w", err)
	}

	inserted, err := c.foods.SeedCatalogue(ctx, foods, resolver.Normalize)
	if err != nil {
		return fmt.Errorf("seed catalogue: %w", err)
	}

	log.Info().Int("entries", len(foods)).Int("written", inserted).Msg("catalogue seeded")
	return nil
}
