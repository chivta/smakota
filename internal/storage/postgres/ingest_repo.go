package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RunCounts struct {
	Fetched  int
	Staged   int
	Rejected int
	Promoted int
}

type StagedItem struct {
	ID           int64
	SourceID     string
	ExternalID   string
	ContentHash  string
	Payload      []byte
	Normalized   domain.Food
	Status       string
	RejectReason string
	RunID        *int64
	CreatedAt    time.Time
}

type PromoteResult struct {
	FoodID   int64
	Promoted bool
	Conflict bool
}

type IngestRepo struct {
	pool *pgxpool.Pool
}

type normalizedFood struct {
	Kind         domain.Kind         `json:"kind"`
	Name         string              `json:"name"`
	Brand        string              `json:"brand"`
	Barcode      string              `json:"barcode"`
	State        domain.State        `json:"state"`
	Nutrition    domain.Nutrition    `json:"nutrition"`
	PackageG     *float64            `json:"package_g"`
	SourceID     string              `json:"source_id"`
	SourceURL    string              `json:"source_url"`
	LicenseClass domain.LicenseClass `json:"license_class"`
	Confidence   int                 `json:"confidence"`
	OwnerUserID  *int64              `json:"owner_user_id"`
	FetchedAt    *time.Time          `json:"fetched_at"`
	Portions     []domain.Portion    `json:"portions"`
	Aliases      []domain.Alias      `json:"aliases"`
}

func NewIngestRepo(pool *pgxpool.Pool) *IngestRepo {
	return &IngestRepo{pool: pool}
}

func (r *IngestRepo) StartRun(ctx context.Context, source string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		insert into ingest_runs (source) values ($1) returning id`, source).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("start ingest run: %w", err)
	}
	return id, nil
}

func (r *IngestRepo) FinishRun(ctx context.Context, runID int64, counts RunCounts, runErr error) error {
	var errorText *string
	if runErr != nil {
		message := runErr.Error()
		errorText = &message
	}
	err := r.pool.QueryRow(ctx, `
		update ingest_runs set
			finished_at = now(), fetched = $2, staged = $3, rejected = $4,
			promoted = $5, error = $6
		where id = $1
		returning id`, runID, counts.Fetched, counts.Staged, counts.Rejected,
		counts.Promoted, errorText).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("finish ingest run: %w", err)
	}
	return nil
}

func (r *IngestRepo) LastSuccess(ctx context.Context, source string) (time.Time, bool, error) {
	var finishedAt time.Time
	err := r.pool.QueryRow(ctx, `
		select finished_at
		from ingest_runs
		where source = $1 and finished_at is not null and error is null
		order by finished_at desc
		limit 1`, source).Scan(&finishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get last successful ingest run: %w", err)
	}
	return finishedAt, true, nil
}

func (r *IngestRepo) Cursor(ctx context.Context, source string) (string, bool, error) {
	var cursor string
	err := r.pool.QueryRow(ctx, `
		select cursor from ingest_cursors where source = $1`, source).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get ingest cursor: %w", err)
	}
	return cursor, true, nil
}

func (r *IngestRepo) SetCursor(ctx context.Context, source, cursor string) error {
	_, err := r.pool.Exec(ctx, `
		insert into ingest_cursors (source, cursor)
		values ($1, $2)
		on conflict (source) do update
		set cursor = excluded.cursor, updated_at = now()`, source, cursor)
	if err != nil {
		return fmt.Errorf("set ingest cursor: %w", err)
	}
	return nil
}

func (r *IngestRepo) Stage(ctx context.Context, items []StagedItem) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin stage items: %w", err)
	}
	defer tx.Rollback(ctx)

	staged := 0
	for _, item := range items {
		normalized, marshalErr := marshalNormalizedFood(item.Normalized)
		if marshalErr != nil {
			return 0, fmt.Errorf("marshal staged item %s/%s: %w", item.SourceID, item.ExternalID, marshalErr)
		}
		result, execErr := tx.Exec(ctx, `
			insert into staging_items (
				source_id, external_id, content_hash, payload, normalized,
				status, reject_reason, run_id
			) values ($1, $2, $3, $4, $5, 'staged', null, $6)
			on conflict (source_id, external_id) do update set
				content_hash = excluded.content_hash,
				payload = excluded.payload,
				normalized = excluded.normalized,
				status = 'staged',
				reject_reason = null,
				run_id = excluded.run_id,
				created_at = now()
			where staging_items.content_hash is distinct from excluded.content_hash`,
			item.SourceID, item.ExternalID, item.ContentHash,
			nullableJSON(item.Payload), normalized, item.RunID)
		if execErr != nil {
			return 0, fmt.Errorf("stage item %s/%s: %w", item.SourceID, item.ExternalID, execErr)
		}
		staged += int(result.RowsAffected())
	}
	err = tx.Commit(ctx)
	if err != nil {
		return 0, fmt.Errorf("commit staged items: %w", err)
	}
	return staged, nil
}

func (r *IngestRepo) Reject(ctx context.Context, sourceID, externalID, reason string, payload []byte) error {
	_, err := r.pool.Exec(ctx, `
		insert into review_queue (raw_input, input_type, reason, payload)
		values ($1, 'ingest', $2, $3)`,
		sourceID+":"+externalID, domain.ReviewIngestReject, nullableJSON(payload))
	if err != nil {
		return fmt.Errorf("reject ingest item: %w", err)
	}
	return nil
}

func (r *IngestRepo) PendingStaged(ctx context.Context, source string, limit int) ([]StagedItem, error) {
	rows, err := r.pool.Query(ctx, `
		select id, source_id, external_id, content_hash, payload, normalized,
			status, coalesce(reject_reason, ''), run_id, created_at
		from staging_items
		where source_id = $1 and status = 'staged'
		order by created_at, id
		limit $2`, source, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending staged items: %w", err)
	}
	defer rows.Close()

	items := make([]StagedItem, 0)
	for rows.Next() {
		var item StagedItem
		var normalized []byte
		err = rows.Scan(
			&item.ID, &item.SourceID, &item.ExternalID, &item.ContentHash,
			&item.Payload, &normalized, &item.Status, &item.RejectReason,
			&item.RunID, &item.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan staged item: %w", err)
		}
		item.Normalized, err = unmarshalNormalizedFood(normalized)
		if err != nil {
			return nil, fmt.Errorf("decode staged item %d: %w", item.ID, err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate staged items: %w", err)
	}
	return items, nil
}

func (r *IngestRepo) Promote(ctx context.Context, item StagedItem) (PromoteResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PromoteResult{}, fmt.Errorf("begin promote staged item: %w", err)
	}
	defer tx.Rollback(ctx)

	f := item.Normalized
	f.SourceID = item.SourceID
	err = tx.QueryRow(ctx, `
		insert into foods (
			kind, name, brand, barcode, state, kcal_100g, protein_100g,
			fat_100g, carbs_100g, package_g, source_id, source_url,
			external_id, license_class, confidence, owner_user_id,
			content_hash, fetched_at
		) values (
			$1, $2, $3, nullif($4, ''), $5, $6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17, $18
		)
		on conflict (source_id, external_id) where external_id is not null
		do update set
			kind = excluded.kind,
			name = excluded.name,
			brand = excluded.brand,
			barcode = excluded.barcode,
			state = excluded.state,
			kcal_100g = excluded.kcal_100g,
			protein_100g = excluded.protein_100g,
			fat_100g = excluded.fat_100g,
			carbs_100g = excluded.carbs_100g,
			package_g = excluded.package_g,
			source_url = excluded.source_url,
			license_class = excluded.license_class,
			confidence = excluded.confidence,
			owner_user_id = excluded.owner_user_id,
			content_hash = excluded.content_hash,
			fetched_at = excluded.fetched_at
		where foods.confidence <= excluded.confidence
		returning id`,
		f.Kind, f.Name, f.Brand, f.Barcode, f.State, f.Nutrition.Kcal,
		f.Nutrition.Protein, f.Nutrition.Fat, f.Nutrition.Carbs, f.PackageG,
		item.SourceID, f.SourceURL, item.ExternalID, f.LicenseClass,
		f.Confidence, f.OwnerUserID, item.ContentHash, f.FetchedAt,
	).Scan(&f.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return r.finishConflict(ctx, tx, item, f)
	}
	if err != nil {
		return PromoteResult{}, fmt.Errorf("upsert promoted food: %w", err)
	}

	err = replaceFoodDetails(ctx, tx, f)
	if err != nil {
		return PromoteResult{}, fmt.Errorf("replace promoted food details: %w", err)
	}
	err = markStagingTx(ctx, tx, item.ID, "promoted", "")
	if err != nil {
		return PromoteResult{}, err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return PromoteResult{}, fmt.Errorf("commit promoted item: %w", err)
	}
	return PromoteResult{FoodID: f.ID, Promoted: true}, nil
}

func (r *IngestRepo) MarkStatus(ctx context.Context, id int64, status, reason string) error {
	err := r.pool.QueryRow(ctx, `
		update staging_items
		set status = $2, reject_reason = nullif($3, '')
		where id = $1
		returning id`, id, status, reason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("mark staged item status: %w", err)
	}
	return nil
}

func (r *IngestRepo) finishConflict(
	ctx context.Context,
	tx pgx.Tx,
	item StagedItem,
	f domain.Food,
) (PromoteResult, error) {
	var foodID int64
	err := tx.QueryRow(ctx, `
		select id from foods where source_id = $1 and external_id = $2`,
		item.SourceID, item.ExternalID).Scan(&foodID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PromoteResult{}, domain.ErrNotFound
	}
	if err != nil {
		return PromoteResult{}, fmt.Errorf("get conflicting food: %w", err)
	}
	const reason = "live row has higher confidence"
	err = markStagingTx(ctx, tx, item.ID, "conflict", reason)
	if err != nil {
		return PromoteResult{}, err
	}
	_, err = tx.Exec(ctx, `
		insert into review_queue (raw_input, input_type, reason, payload)
		values ($1, 'ingest', $2, $3)`,
		f.Name, domain.ReviewIngestConflict, nullableJSON(item.Payload))
	if err != nil {
		return PromoteResult{}, fmt.Errorf("queue ingest conflict: %w", err)
	}
	err = tx.Commit(ctx)
	if err != nil {
		return PromoteResult{}, fmt.Errorf("commit ingest conflict: %w", err)
	}
	return PromoteResult{FoodID: foodID, Conflict: true}, nil
}

func markStagingTx(ctx context.Context, tx pgx.Tx, id int64, status, reason string) error {
	err := tx.QueryRow(ctx, `
		update staging_items
		set status = $2, reject_reason = nullif($3, '')
		where id = $1
		returning id`, id, status, reason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("mark staged item status: %w", err)
	}
	return nil
}

func marshalNormalizedFood(f domain.Food) ([]byte, error) {
	value := normalizedFood{
		Kind: f.Kind, Name: f.Name, Brand: f.Brand, Barcode: f.Barcode,
		State: f.State, Nutrition: f.Nutrition, PackageG: f.PackageG,
		SourceID: f.SourceID, SourceURL: f.SourceURL,
		LicenseClass: f.LicenseClass, Confidence: f.Confidence,
		OwnerUserID: f.OwnerUserID, FetchedAt: f.FetchedAt,
		Portions: f.Portions, Aliases: f.Aliases,
	}
	return json.Marshal(value)
}

func unmarshalNormalizedFood(data []byte) (domain.Food, error) {
	var value normalizedFood
	err := json.Unmarshal(data, &value)
	if err != nil {
		return domain.Food{}, err
	}
	return domain.Food{
		Kind: value.Kind, Name: value.Name, Brand: value.Brand,
		Barcode: value.Barcode, State: value.State, Nutrition: value.Nutrition,
		PackageG: value.PackageG, SourceID: value.SourceID,
		SourceURL: value.SourceURL, LicenseClass: value.LicenseClass,
		Confidence: value.Confidence, OwnerUserID: value.OwnerUserID,
		FetchedAt: value.FetchedAt, Portions: value.Portions, Aliases: value.Aliases,
	}, nil
}

func nullableJSON(payload []byte) []byte {
	if len(payload) == 0 {
		return []byte("null")
	}
	if json.Valid(payload) {
		return payload
	}
	encoded, err := json.Marshal(string(payload))
	if err != nil {
		return []byte("null")
	}
	return encoded
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
