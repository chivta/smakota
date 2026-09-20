// Package scheduler runs the recurring jobs in process, sharing the config, the
// logger and the database pool with everything else. Each run is recorded in
// ingest_runs, so "is the data fresh" has one answer.
package scheduler

import (
	"context"
	"time"

	"github.com/arvlas/calbot/internal/metrics"
	"github.com/rs/zerolog/log"
)

// tick is the resolution the schedule is evaluated at. The shortest job runs
// every 10 minutes, so a minute is finer than anything needs.
const tick = time.Minute

// Job is one scheduled unit of work.
type Job struct {
	Name string
	// Due reports whether this job should start at t, evaluated in the
	// configured location.
	Due func(t time.Time) bool
	// Timeout bounds one run. A job still going when its successor comes due is
	// skipped rather than doubled, by the lock below.
	Timeout time.Duration
	Run     func(ctx context.Context) error
}

// locker prevents a job from overlapping itself. Postgres advisory locks make
// this hold even if a second process is ever started by mistake.
type locker interface {
	TryLock(ctx context.Context, key string) (bool, func(), error)
}

type Scheduler struct {
	jobs []Job
	lock locker
	loc  *time.Location
}

func New(loc *time.Location, lock locker, jobs []Job) *Scheduler {
	return &Scheduler{jobs: jobs, lock: lock, loc: loc}
}

// Run evaluates the schedule every tick until ctx is cancelled. It returns once
// every in-flight job has acknowledged cancellation.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	log.Info().Int("jobs", len(s.jobs)).Msg("scheduler started")

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("scheduler stopping")
			return
		case now := <-ticker.C:
			s.dispatch(ctx, now.In(s.loc))
		}
	}
}

func (s *Scheduler) dispatch(ctx context.Context, now time.Time) {
	for _, job := range s.jobs {
		if !job.Due(now) {
			continue
		}
		go s.runOne(ctx, job)
	}
}

// runOne takes the job's lock, runs it under its timeout, and logs the outcome
// once. A job that cannot take the lock is still running from a previous tick.
func (s *Scheduler) runOne(ctx context.Context, job Job) {
	acquired, release, err := s.lock.TryLock(ctx, job.Name)
	if err != nil {
		log.Error().Err(err).Str("job", job.Name).Msg("could not take job lock")
		return
	}
	if !acquired {
		log.Info().Str("job", job.Name).Msg("previous run still in flight, skipping")
		return
	}
	defer release()

	runCtx, cancel := context.WithTimeout(ctx, job.Timeout)
	defer cancel()

	started := time.Now()
	err = job.Run(runCtx)
	elapsed := time.Since(started)

	if err != nil {
		log.Error().Err(err).Str("job", job.Name).Dur("elapsed", elapsed).Msg("job failed")
		return
	}

	metrics.IngestAge.WithLabelValues(job.Name).Set(0)
	log.Info().Str("job", job.Name).Dur("elapsed", elapsed).Msg("job finished")
}
