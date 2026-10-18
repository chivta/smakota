package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

// adapterTimeout bounds one adapter so a hung provider cannot consume the
// window of the adapters after it.
const (
	adapterTimeout = 2 * time.Hour
	closeTimeout   = time.Minute
)

// SourceResult is the outcome of one adapter within RunAll.
type SourceResult struct {
	Source  string
	Counts  Counts
	Elapsed time.Duration
	Err     error
}

// RunAll runs every adapter in order. A failing or panicking adapter is
// logged and recorded, and the next one still runs; only a cancelled ctx
// stops the loop. Adapters not reached are absent from the result.
func (r *Runner) RunAll(ctx context.Context, adapters []Adapter) []SourceResult {
	results := make([]SourceResult, 0, len(adapters))
	for _, adapter := range adapters {
		if ctx.Err() != nil {
			break
		}
		result := r.runIsolated(ctx, adapter)
		event := log.Info()
		if result.Err != nil {
			event = log.Error().Err(result.Err)
		}
		event.Str("source", result.Source).
			Int("fetched", result.Counts.Fetched).
			Int("staged", result.Counts.Staged).
			Int("rejected", result.Counts.Rejected).
			Int("promoted", result.Counts.Promoted).
			Dur("elapsed", result.Elapsed).
			Msg("ingest source finished")
		results = append(results, result)
	}
	return results
}

func (r *Runner) runIsolated(ctx context.Context, adapter Adapter) (result SourceResult) {
	result.Source = adapter.Name()
	started := time.Now()
	counter := &countingStore{store: r.store}
	defer func() {
		result.Elapsed = time.Since(started)
		result.Counts = counter.counts
		if recovered := recover(); recovered != nil {
			result.Err = fmt.Errorf("%s adapter panicked: %v", result.Source, recovered)
			counter.abandon(ctx, result.Err)
		}
	}()

	runCtx, cancel := context.WithTimeout(ctx, adapterTimeout)
	defer cancel()
	result.Err = (&Runner{store: counter}).Run(runCtx, adapter)
	return result
}

// countingStore remembers the counts and ID of the run it sees, because Run
// returns only an error.
type countingStore struct {
	store
	runID    int64
	finished bool
	counts   Counts
}

func (s *countingStore) StartRun(ctx context.Context, source string) (int64, error) {
	runID, err := s.store.StartRun(ctx, source)
	s.runID = runID
	return runID, err
}

func (s *countingStore) FinishRun(ctx context.Context, runID int64, counts Counts, runErr error) error {
	s.finished = true
	s.counts = counts
	return s.store.FinishRun(ctx, runID, counts, runErr)
}

// abandon closes the run row a panic left open. It uses a context detached
// from cancellation so a timed-out run can still be closed.
func (s *countingStore) abandon(ctx context.Context, cause error) {
	if s.runID == 0 || s.finished {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	err := s.store.FinishRun(closeCtx, s.runID, s.counts, cause)
	if err != nil {
		log.Error().Err(err).Msg("close panicked ingest run")
	}
}
