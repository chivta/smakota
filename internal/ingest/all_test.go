package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/arvlas/calbot/internal/domain"
)

// fakeAdapter emits one row, fails, or panics, so a RunAll pass can be checked
// against each kind of provider.
type fakeAdapter struct {
	name  string
	emit  bool
	err   error
	panic bool
}

func (a fakeAdapter) Name() string { return a.name }

func (a fakeAdapter) Provenance() Provenance {
	return Provenance{LicenseClass: domain.LicenseLookupOnly, Confidence: domain.ConfidenceScraped}
}

func (a fakeAdapter) Fetch(_ context.Context, _ string, emit func(Raw) error) (string, error) {
	if a.panic {
		panic("provider returned a shape nobody expected")
	}
	if a.emit {
		err := emit(Raw{
			ExternalID: a.name + "-1",
			Product: RawProduct{
				Name: "Тест", Kcal: "100", Protein: "10", Fat: "5", Carbs: "5",
			},
		})
		if err != nil {
			return "", err
		}
	}
	return "", a.err
}

// fakeStore records what reached it without needing a database.
type fakeStore struct {
	nextRunID int64
	staged    map[string]int
	finished  map[int64]error
	cursors   map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		staged:   map[string]int{},
		finished: map[int64]error{},
		cursors:  map[string]string{},
	}
}

func (s *fakeStore) StartRun(_ context.Context, _ string) (int64, error) {
	s.nextRunID++
	return s.nextRunID, nil
}

func (s *fakeStore) FinishRun(_ context.Context, runID int64, _ Counts, runErr error) error {
	s.finished[runID] = runErr
	return nil
}

func (s *fakeStore) Cursor(context.Context, string) (string, bool, error) { return "", false, nil }
func (s *fakeStore) SetCursor(_ context.Context, source, cursor string) error {
	s.cursors[source] = cursor
	return nil
}

func (s *fakeStore) Stage(_ context.Context, items []StagedItem) (int, error) {
	for _, item := range items {
		s.staged[item.SourceID]++
	}
	return len(items), nil
}

func (s *fakeStore) Reject(context.Context, string, string, string, []byte) error { return nil }

func (s *fakeStore) PendingStaged(context.Context, string, int) ([]StagedItem, error) {
	return nil, nil
}

func (s *fakeStore) Promote(context.Context, StagedItem) (PromoteResult, error) {
	return PromoteResult{}, nil
}

func (s *fakeStore) MarkStatus(context.Context, int64, string, string) error { return nil }

// A failing provider and a panicking one must not stop the providers queued
// behind them, which is the whole reason RunAll exists.
func TestRunAllIsolatesFailures(t *testing.T) {
	boom := errors.New("provider is down")
	adapters := []Adapter{
		fakeAdapter{name: "first", emit: true},
		fakeAdapter{name: "broken", err: boom},
		fakeAdapter{name: "panicky", panic: true},
		fakeAdapter{name: "last", emit: true},
	}

	store := newFakeStore()
	results := (&Runner{store: store}).RunAll(context.Background(), adapters)

	if len(results) != 4 {
		t.Fatalf("got %d results, want 4: a failure stopped the loop", len(results))
	}

	byName := map[string]SourceResult{}
	for _, r := range results {
		byName[r.Source] = r
	}

	if byName["first"].Err != nil {
		t.Errorf("first failed: %v", byName["first"].Err)
	}
	if !errors.Is(byName["broken"].Err, boom) {
		t.Errorf("broken error = %v, want the provider's error", byName["broken"].Err)
	}
	if byName["panicky"].Err == nil {
		t.Error("panicky succeeded, want the panic recovered into an error")
	}
	if byName["last"].Err != nil {
		t.Errorf("last failed: %v, want it to run despite the two before it", byName["last"].Err)
	}

	for _, name := range []string{"first", "last"} {
		if store.staged[name] != 1 {
			t.Errorf("%s staged %d rows, want 1", name, store.staged[name])
		}
	}

	// Every adapter opens a run row, and all four must be closed, including
	// the one whose adapter panicked.
	if len(store.finished) != 4 {
		t.Errorf("closed %d run rows, want 4: a panicked run was left open", len(store.finished))
	}
}

// A cancelled context stops the loop rather than burning through the rest.
func TestRunAllStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := (&Runner{store: newFakeStore()}).RunAll(ctx, []Adapter{
		fakeAdapter{name: "first", emit: true},
		fakeAdapter{name: "second", emit: true},
	})

	if len(results) != 0 {
		t.Errorf("ran %d adapters on a cancelled context, want 0", len(results))
	}
}

// A Raw carrying only a cursor saves progress without staging a row, which is
// how an adapter walking a large index avoids re-reading pages that held no
// products.
func TestCheckpointSavesCursorWithoutStaging(t *testing.T) {
	store := newFakeStore()
	runner := &Runner{store: store}

	adapter := checkpointAdapter{}
	err := runner.Run(context.Background(), adapter)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if store.cursors["checkpointer"] != "page-9" {
		t.Errorf("cursor = %q, want the last checkpoint", store.cursors["checkpointer"])
	}
	if store.staged["checkpointer"] != 1 {
		t.Errorf("staged %d rows, want 1: a checkpoint must not stage", store.staged["checkpointer"])
	}
}

type checkpointAdapter struct{}

func (checkpointAdapter) Name() string { return "checkpointer" }

func (checkpointAdapter) Provenance() Provenance {
	return Provenance{LicenseClass: domain.LicenseLookupOnly, Confidence: domain.ConfidenceScraped}
}

func (checkpointAdapter) Fetch(_ context.Context, _ string, emit func(Raw) error) (string, error) {
	err := emit(Raw{Cursor: "page-8", Checkpoint: true})
	if err != nil {
		return "", err
	}
	err = emit(Raw{
		ExternalID: "real-1",
		Product:    RawProduct{Name: "Тест", Kcal: "100", Protein: "10", Fat: "5", Carbs: "5"},
	})
	if err != nil {
		return "", err
	}
	return "", emit(Raw{Cursor: "page-9", Checkpoint: true})
}
