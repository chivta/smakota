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
	Promoted int
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
	Promote(ctx context.Context, item StagedItem) (PromoteResult, error)
	MarkStatus(ctx context.Context, stagingID int64, status, reason string) error
}

// Runner executes the ingestion phases for one adapter.
type Runner struct {
	store store
}

// NewRunner constructs a runner using the catalogue's ingest repository.
func NewRunner(store store) *Runner {
	return &Runner{store: store}
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

func (r *Runner) run(ctx context.Context, runID int64, adapter Adapter, counts *Counts) error {
	source := adapter.Name()

	// Promote what a previous run staged before fetching more. Promotion
	// otherwise happens only after a complete fetch, so an interrupted crawl
	// would leave its rows staged and invisible until the next full pass.
	err := r.promotePending(ctx, source, counts)
	if err != nil {
		return err
	}

	cursor, _, err := r.store.Cursor(ctx, source)
	if err != nil {
		return fmt.Errorf("read %s cursor: %w", source, err)
	}

	nextCursor, err := adapter.Fetch(ctx, cursor, func(raw Raw) error {
		// A checkpoint is progress only. Inferring it from empty fields would
		// swallow a malformed product row that belongs in the reject queue.
		if raw.Checkpoint {
			return r.store.SetCursor(ctx, source, raw.Cursor)
		}
		counts.Fetched++
		countRows(source, stageFetched, 1)
		normalized, normalizeErr := Normalize(source, adapter.Provenance(), raw)
		if normalizeErr != nil {
			return r.reject(ctx, runID, source, raw, normalizeErr, counts)
		}

		validateErr := Validate(normalized.Nutrition)
		if validateErr != nil {
			return r.reject(ctx, runID, source, raw, validateErr, counts)
		}

		item, itemErr := newStagedItem(runID, source, raw, normalized)
		if itemErr != nil {
			return itemErr
		}
		staged, stageErr := r.store.Stage(ctx, []StagedItem{item})
		if stageErr != nil {
			return fmt.Errorf("stage %s product %s: %w", source, raw.ExternalID, stageErr)
		}
		counts.Staged += staged
		countRows(source, stageStaged, staged)
		if raw.Cursor != "" {
			setErr := r.store.SetCursor(ctx, source, raw.Cursor)
			if setErr != nil {
				return fmt.Errorf("save %s cursor: %w", source, setErr)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("fetch %s: %w", source, err)
	}
	if nextCursor != "" {
		err = r.store.SetCursor(ctx, source, nextCursor)
		if err != nil {
			return fmt.Errorf("save %s cursor: %w", source, err)
		}
	}

	return r.promotePending(ctx, source, counts)
}

const promoteBatchSize = 500

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
		for _, item := range pending {
			result, promoteErr := r.store.Promote(ctx, item)
			if promoteErr != nil {
				return fmt.Errorf("promote %s product %s: %w", source, item.ExternalID, promoteErr)
			}
			if result.Promoted {
				counts.Promoted++
				countRows(source, stagePromoted, 1)
				continue
			}
			if !result.Conflict {
				markErr := r.store.MarkStatus(ctx, item.ID, "conflict", "promotion produced no result")
				if markErr != nil {
					return fmt.Errorf("mark %s product %s conflict: %w", source, item.ExternalID, markErr)
				}
			}
		}
	}
}

func (r *Runner) reject(ctx context.Context, runID int64, source string, raw Raw, cause error, counts *Counts) error {
	err := r.store.Reject(ctx, source, raw.ExternalID, cause.Error(), raw.Payload)
	if err != nil {
		return fmt.Errorf("reject %s product %s: %w", source, raw.ExternalID, err)
	}
	counts.Rejected++
	countRows(source, stageRejected, 1)
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
