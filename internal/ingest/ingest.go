// Package ingest runs catalogue imports through normalization, validation,
// staging, and promotion.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/metrics"
)

// Adapter fetches source rows. It does not have access to catalogue storage.
type Adapter interface {
	Name() string
	// Provenance is the adapter's own, so a new source is a new package rather
	// than an edit to the shared normalizer.
	Provenance() Provenance
	Fetch(ctx context.Context, cursor string, emit func(Raw) error) (nextCursor string, err error)
}

// Provenance is what a source's rows are allowed to do: whether they may leave
// the host, and what they may overwrite during promote.
type Provenance struct {
	LicenseClass domain.LicenseClass
	Confidence   int
}

// RawProduct is the source-shaped product data required by the shared
// normalizer. Payload on Raw remains the untouched source response.
type RawProduct struct {
	Name       string
	Brand      string
	Barcode    string
	Quantity   string
	Kcal       string
	Kilojoules string
	Protein    string
	Fat        string
	Carbs      string
}

// cursorKeyed is implemented by an adapter whose jobs keep separate progress
// under one source name, as OFF's delta and full runs do.
type cursorKeyed interface {
	CursorKey() string
}

func cursorKey(adapter Adapter) string {
	keyed, ok := adapter.(cursorKeyed)
	if ok {
		return keyed.CursorKey()
	}
	return adapter.Name()
}

// Raw is one fetched source row. Cursor is optional and allows page-level
// progress to be persisted during a long Fetch call.
//
// Setting Checkpoint saves progress without staging anything, which adapters
// walking a large index use so a killed run resumes where it stopped instead of
// re-reading the pages that held no products.
type Raw struct {
	ExternalID string
	Payload    []byte
	SourceURL  string
	Cursor     string
	Product    RawProduct
	Checkpoint bool
}

// Normalized is a product ready for validation and staging.
type Normalized = domain.Food

// StagedItem is the storage-neutral representation exchanged with the ingest
// repository during stage and promote.
type StagedItem struct {
	ID          int64
	SourceID    string
	ExternalID  string
	ContentHash string
	Payload     []byte
	Normalized  domain.Food
	RunID       *int64
}

// Pipeline stages, as the stage label of metrics.IngestRows.
const (
	stageFetched  = "fetched"
	stageStaged   = "staged"
	stageRejected = "rejected"
	stagePromoted = "promoted"
)

func countRows(source, stage string, n int) {
	metrics.IngestRows.WithLabelValues(source, stage).Add(float64(n))
}

// Counts are persisted on the ingest run when it finishes.
type Counts struct {
	Fetched  int
	Staged   int
	Rejected int
	// RejectedDropped is the part of Rejected that was counted but not stored
	// for review, because its reason had already reached the per-run cap.
	RejectedDropped int
	Promoted        int
}

// PromoteResult describes whether promotion changed the catalogue or found a
// higher-confidence conflict.
type PromoteResult struct {
	FoodID   int64
	Promoted bool
	Conflict bool
}

// Store is declared at its consumer. The Postgres ingest repository satisfies
// it without the ingestion package depending on the storage package.
type store interface {
	StartRun(ctx context.Context, source string) (int64, error)
	FinishRun(ctx context.Context, runID int64, counts Counts, runErr error) error
	Cursor(ctx context.Context, source string) (string, bool, error)
	SetCursor(ctx context.Context, source, cursor string) error
	Stage(ctx context.Context, items []StagedItem) (int, error)
	Reject(ctx context.Context, source, externalID, reason string, payload []byte) error
	PendingStaged(ctx context.Context, source string, limit int) ([]StagedItem, error)
	// PromoteBatch returns one result per item, in order.
	PromoteBatch(ctx context.Context, items []StagedItem) ([]PromoteResult, error)
	MarkStatus(ctx context.Context, stagingID int64, status, reason string) error
}

// Batch sizes. Staging and promotion each run one transaction per batch, so a
// million-row source costs thousands of transactions rather than millions.
const (
	stageBatchSize   = 1000
	promoteBatchSize = 1000

	// flushTimeout bounds the final flush of an interrupted run, which has to
	// outlive the cancelled context that interrupted it.
	flushTimeout = time.Minute
)

// Runner executes the ingestion phases for one adapter.
type Runner struct {
	store store
	// rejectCap is how many rejected rows per distinct reason a run stores in
	// the review queue. Every rejection is counted regardless.
	rejectCap int
}

// NewRunner constructs a runner using the catalogue's ingest repository.
func NewRunner(store store, rejectCap int) *Runner {
	return &Runner{store: store, rejectCap: rejectCap}
}

// Run fetches and stages the source, then promotes all pending staged rows.
func (r *Runner) Run(ctx context.Context, adapter Adapter) error {
	source := adapter.Name()
	runID, err := r.store.StartRun(ctx, source)
	if err != nil {
		return fmt.Errorf("start %s ingest run: %w", source, err)
	}

	var counts Counts
	runErr := r.run(ctx, runID, adapter, &counts)
	finishErr := r.store.FinishRun(ctx, runID, counts, runErr)
	if finishErr != nil {
		finishErr = fmt.Errorf("finish %s ingest run: %w", source, finishErr)
	}
	return errors.Join(runErr, finishErr)
}

// batch buffers staged rows until a flush. cursor is the progress the buffered
// rows account for; it is saved only after they are staged, so a killed run
// never resumes past rows that were not written.
type batch struct {
	items  []StagedItem
	cursor string
}

func (r *Runner) run(ctx context.Context, runID int64, adapter Adapter, counts *Counts) error {
	source := adapter.Name()
	key := cursorKey(adapter)

	// Promote what a previous run staged before fetching more, so an
	// interrupted crawl does not leave its rows staged and invisible.
	err := r.promotePending(ctx, source, counts)
	if err != nil {
		return err
	}

	cursor, _, err := r.store.Cursor(ctx, key)
	if err != nil {
		return fmt.Errorf("read %s cursor: %w", source, err)
	}

	var buffered batch
	rejects := newRejectLimiter(r.rejectCap)
	nextCursor, err := adapter.Fetch(ctx, cursor, func(raw Raw) error {
		// A checkpoint is progress only. Inferring it from empty fields would
		// swallow a malformed product row that belongs in the reject queue.
		if raw.Checkpoint {
			buffered.cursor = raw.Cursor
			return r.flush(ctx, source, key, &buffered, counts)
		}
		counts.Fetched++
		countRows(source, stageFetched, 1)
		normalized, normalizeErr := Normalize(source, adapter.Provenance(), raw)
		if normalizeErr != nil {
			return r.reject(ctx, rejects, source, raw, normalizeErr, counts)
		}

		validateErr := Validate(normalized.Nutrition)
		if validateErr != nil {
			return r.reject(ctx, rejects, source, raw, validateErr, counts)
		}

		item, itemErr := newStagedItem(runID, source, raw, normalized)
		if itemErr != nil {
			return itemErr
		}
		buffered.items = append(buffered.items, item)
		if raw.Cursor != "" {
			buffered.cursor = raw.Cursor
		}
		if len(buffered.items) < stageBatchSize {
			return nil
		}
		return r.flush(ctx, source, key, &buffered, counts)
	})
	if err != nil {
		// Keep what was fetched before the failure, including when the cause
		// is the cancellation that is stopping the run.
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
		defer cancel()
		flushErr := r.flush(flushCtx, source, key, &buffered, counts)
		return errors.Join(fmt.Errorf("fetch %s: %w", source, err), flushErr)
	}
	err = r.flush(ctx, source, key, &buffered, counts)
	if err != nil {
		return err
	}
	if nextCursor != "" {
		err = r.store.SetCursor(ctx, key, nextCursor)
		if err != nil {
			return fmt.Errorf("save %s cursor: %w", source, err)
		}
	}

	return r.promotePending(ctx, source, counts)
}

// flush stages the buffered rows, saves the progress they account for, and
// promotes them.
func (r *Runner) flush(ctx context.Context, source, key string, buffered *batch, counts *Counts) error {
	if len(buffered.items) > 0 {
		staged, err := r.store.Stage(ctx, buffered.items)
		if err != nil {
			return fmt.Errorf("stage %d %s products: %w", len(buffered.items), source, err)
		}
		counts.Staged += staged
		countRows(source, stageStaged, staged)
		buffered.items = buffered.items[:0]
	}
	if buffered.cursor != "" {
		err := r.store.SetCursor(ctx, key, buffered.cursor)
		if err != nil {
			return fmt.Errorf("save %s cursor: %w", source, err)
		}
		buffered.cursor = ""
	}
	return r.promotePending(ctx, source, counts)
}

// promotePending publishes every staged row for a source. Promote is the only
// writer of the catalogue, and it refuses to overwrite a higher-confidence row.
func (r *Runner) promotePending(ctx context.Context, source string, counts *Counts) error {
	for {
		pending, pendingErr := r.store.PendingStaged(ctx, source, promoteBatchSize)
		if pendingErr != nil {
			return fmt.Errorf("list pending %s products: %w", source, pendingErr)
		}
		if len(pending) == 0 {
			return nil
		}
		results, promoteErr := r.store.PromoteBatch(ctx, pending)
		if promoteErr != nil {
			return fmt.Errorf("promote %d %s products: %w", len(pending), source, promoteErr)
		}
		for i, result := range results {
			if result.Promoted {
				counts.Promoted++
				countRows(source, stagePromoted, 1)
				continue
			}
			if !result.Conflict {
				markErr := r.store.MarkStatus(ctx, pending[i].ID, "conflict", "promotion produced no result")
				if markErr != nil {
					return fmt.Errorf("mark %s product %s conflict: %w", source, pending[i].ExternalID, markErr)
				}
			}
		}
	}
}

// rejectLimiter caps how many rejections per distinct reason a run stores.
type rejectLimiter struct {
	limit  int
	stored map[string]int
}

func newRejectLimiter(limit int) *rejectLimiter {
	return &rejectLimiter{limit: limit, stored: map[string]int{}}
}

// take reports whether one more rejection of this reason may be stored.
func (l *rejectLimiter) take(reason string) bool {
	key := reasonKey(reason)
	if l.stored[key] >= l.limit {
		return false
	}
	l.stored[key]++
	return true
}

var (
	reasonNumber = regexp.MustCompile(`[-+]?\d+(?:[.,]\d+)?`)
	reasonQuoted = regexp.MustCompile(`"[^"]*"`)
)

// reasonKey folds the values out of a reason, so "stated 120.50 kcal" and
// "stated 87 kcal" count as one reason.
func reasonKey(reason string) string {
	return reasonNumber.ReplaceAllString(reasonQuoted.ReplaceAllString(reason, `"?"`), "#")
}

func (r *Runner) reject(ctx context.Context, limiter *rejectLimiter, source string, raw Raw, cause error, counts *Counts) error {
	counts.Rejected++
	countRows(source, stageRejected, 1)
	if !limiter.take(cause.Error()) {
		counts.RejectedDropped++
		return nil
	}
	err := r.store.Reject(ctx, source, raw.ExternalID, cause.Error(), raw.Payload)
	if err != nil {
		return fmt.Errorf("reject %s product %s: %w", source, raw.ExternalID, err)
	}
	return nil
}

func newStagedItem(runID int64, source string, raw Raw, normalized Normalized) (StagedItem, error) {
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return StagedItem{}, fmt.Errorf("marshal normalized %s product %s: %w", source, raw.ExternalID, err)
	}
	hash := sha256.Sum256(encoded)
	return StagedItem{
		SourceID:    source,
		ExternalID:  raw.ExternalID,
		ContentHash: hex.EncodeToString(hash[:]),
		Payload:     raw.Payload,
		Normalized:  normalized,
		RunID:       &runID,
	}, nil
}
