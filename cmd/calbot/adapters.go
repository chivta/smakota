package main

import (
	"context"

	"github.com/arvlas/calbot/internal/ingest"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/arvlas/calbot/internal/storage/postgres"
)

// The ingest framework and the resolver each declare the storage they need on
// their own side, so neither imports the repository package. The shapes agree;
// the named types do not. These two adapters are the seam.

type searchAdapter struct {
	repo *postgres.FoodRepo
}

func (a searchAdapter) Search(ctx context.Context, p resolver.SearchParams) ([]resolver.Candidate, error) {
	var userID *int64
	if p.UserID != 0 {
		id := p.UserID
		userID = &id
	}

	found, err := a.repo.Search(ctx, postgres.SearchParams{
		Query:  p.Query,
		UserID: userID,
		Limit:  p.Limit,
	})
	if err != nil {
		return nil, err
	}

	out := make([]resolver.Candidate, len(found))
	for i, c := range found {
		out[i] = resolver.Candidate{Food: c.Food, Score: c.Score}
	}
	return out, nil
}

type ingestStore struct {
	repo *postgres.IngestRepo
}

func (s ingestStore) StartRun(ctx context.Context, source string) (int64, error) {
	return s.repo.StartRun(ctx, source)
}

func (s ingestStore) FinishRun(ctx context.Context, runID int64, counts ingest.Counts, runErr error) error {
	return s.repo.FinishRun(ctx, runID, postgres.RunCounts(counts), runErr)
}

func (s ingestStore) Cursor(ctx context.Context, source string) (string, bool, error) {
	return s.repo.Cursor(ctx, source)
}

func (s ingestStore) SetCursor(ctx context.Context, source, cursor string) error {
	return s.repo.SetCursor(ctx, source, cursor)
}

func (s ingestStore) Stage(ctx context.Context, items []ingest.StagedItem) (int, error) {
	converted := make([]postgres.StagedItem, len(items))
	for i, item := range items {
		converted[i] = toRepoItem(item)
	}
	return s.repo.Stage(ctx, converted)
}

func (s ingestStore) Reject(ctx context.Context, source, externalID, reason string, payload []byte) error {
	return s.repo.Reject(ctx, source, externalID, reason, payload)
}

func (s ingestStore) PendingStaged(ctx context.Context, source string, limit int) ([]ingest.StagedItem, error) {
	found, err := s.repo.PendingStaged(ctx, source, limit)
	if err != nil {
		return nil, err
	}

	out := make([]ingest.StagedItem, len(found))
	for i, item := range found {
		out[i] = ingest.StagedItem{
			ID:          item.ID,
			SourceID:    item.SourceID,
			ExternalID:  item.ExternalID,
			ContentHash: item.ContentHash,
			Payload:     item.Payload,
			Normalized:  item.Normalized,
			RunID:       item.RunID,
		}
	}
	return out, nil
}

func (s ingestStore) Promote(ctx context.Context, item ingest.StagedItem) (ingest.PromoteResult, error) {
	result, err := s.repo.Promote(ctx, toRepoItem(item))
	if err != nil {
		return ingest.PromoteResult{}, err
	}
	return ingest.PromoteResult(result), nil
}

func (s ingestStore) MarkStatus(ctx context.Context, stagingID int64, status, reason string) error {
	return s.repo.MarkStatus(ctx, stagingID, status, reason)
}

func toRepoItem(item ingest.StagedItem) postgres.StagedItem {
	return postgres.StagedItem{
		ID:          item.ID,
		SourceID:    item.SourceID,
		ExternalID:  item.ExternalID,
		ContentHash: item.ContentHash,
		Payload:     item.Payload,
		Normalized:  item.Normalized,
		RunID:       item.RunID,
	}
}
