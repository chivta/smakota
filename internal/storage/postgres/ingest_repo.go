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
	// RejectedDropped is the part of Rejected that was counted but not stored
	// in review_queue.
	RejectedDropped int
	Promoted        int
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
			promoted = $5, error = $6, rejected_dropped = $7
		where id = $1
		returning id`, runID, counts.Fetched, counts.Staged, counts.Rejected,
		counts.Promoted, errorText, counts.RejectedDropped).Scan(&runID)
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

// Stage upserts the batch with one multi-row insert. An unchanged row (same
// content hash) is left alone and not counted.
func (r *IngestRepo) Stage(ctx context.Context, items []StagedItem) (int, error) {
	items = lastPerKey(items)
	sources := make([]string, len(items))
	externalIDs := make([]string, len(items))
	hashes := make([]string, len(items))
	payloads := make([]string, len(items))
	normalizedJSON := make([]string, len(items))
	runIDs := make([]*int64, len(items))
	for i, item := range items {
		normalized, err := marshalNormalizedFood(item.Normalized)
		if err != nil {
			return 0, fmt.Errorf("marshal staged item %s/%s: %w", item.SourceID, item.ExternalID, err)
		}
		sources[i] = item.SourceID
		externalIDs[i] = item.ExternalID
		hashes[i] = item.ContentHash
		payloads[i] = string(nullableJSON(item.Payload))
		normalizedJSON[i] = string(normalized)
		runIDs[i] = item.RunID
	}

	result, err := r.pool.Exec(ctx, `
		insert into staging_items (
			source_id, external_id, content_hash, payload, normalized,
			status, reject_reason, run_id
		)
		select source_id, external_id, content_hash, payload::jsonb,
			normalized::jsonb, 'staged', null, run_id
		from unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::text[], $6::bigint[])
			as t(source_id, external_id, content_hash, payload, normalized, run_id)
		on conflict (source_id, external_id) do update set
			content_hash = excluded.content_hash,
			payload = excluded.payload,
			normalized = excluded.normalized,
			status = 'staged',
			reject_reason = null,
			run_id = excluded.run_id,
			created_at = now()
		where staging_items.content_hash is distinct from excluded.content_hash`,
		sources, externalIDs, hashes, payloads, normalizedJSON, runIDs)
	if err != nil {
		return 0, fmt.Errorf("stage %d items: %w", len(items), err)
	}
	return int(result.RowsAffected()), nil
}

// lastPerKey drops earlier copies of a source/external ID pair, which a single
// upsert statement refuses to see twice.
func lastPerKey(items []StagedItem) []StagedItem {
	last := make(map[[2]string]int, len(items))
	for i, item := range items {
		last[[2]string{item.SourceID, item.ExternalID}] = i
	}
	if len(last) == len(items) {
		return items
	}
	kept := make([]StagedItem, 0, len(last))
	for i, item := range items {
		if last[[2]string{item.SourceID, item.ExternalID}] == i {
			kept = append(kept, item)
		}
	}
	return kept
}

func (r *IngestRepo) Reject(ctx context.Context, sourceID, externalID, reason string, payload []byte) error {
	_, err := r.pool.Exec(ctx, `
		insert into review_queue (raw_input, input_type, reason, detail, payload)
		values ($1, 'ingest', $2, $3, $4)`,
		sourceID+":"+externalID, domain.ReviewIngestReject, reason, nullableJSON(payload))
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
		order by id
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

const conflictReason = "live row has higher confidence"

// PromoteBatch publishes the batch in one transaction and returns a result per
// item, in order. A staged row never overwrites a live row of higher
// confidence: that row is marked a conflict and queued for review instead.
func (r *IngestRepo) PromoteBatch(ctx context.Context, items []StagedItem) ([]PromoteResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin promote batch: %w", err)
	}
	defer tx.Rollback(ctx)

	foodIDs, err := upsertFoods(ctx, tx, items)
	if err != nil {
		return nil, err
	}

	results := make([]PromoteResult, len(items))
	var promoted []domain.Food
	var conflicts []int
	for i, item := range items {
		id, ok := foodIDs[promoteKey(item.SourceID, item.ExternalID)]
		if !ok {
			conflicts = append(conflicts, i)
			continue
		}
		results[i] = PromoteResult{FoodID: id, Promoted: true}
		f := item.Normalized
		f.ID = id
		f.SourceID = item.SourceID
		promoted = append(promoted, f)
	}

	err = replaceFoodDetailsBatch(ctx, tx, promoted)
	if err != nil {
		return nil, fmt.Errorf("replace promoted food details: %w", err)
	}
	err = r.recordConflicts(ctx, tx, items, conflicts, results)
	if err != nil {
		return nil, err
	}

	ids := make([]int64, len(items))
	statuses := make([]string, len(items))
	reasons := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
		statuses[i] = "promoted"
		if !results[i].Promoted {
			statuses[i] = "conflict"
			reasons[i] = conflictReason
		}
	}
	_, err = tx.Exec(ctx, `
		update staging_items s
		set status = t.status, reject_reason = nullif(t.reason, '')
		from unnest($1::bigint[], $2::text[], $3::text[]) as t(id, status, reason)
		where s.id = t.id`, ids, statuses, reasons)
	if err != nil {
		return nil, fmt.Errorf("mark promoted batch: %w", err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit promote batch: %w", err)
	}
	return results, nil
}

func promoteKey(sourceID, externalID string) string {
	return sourceID + "\x00" + externalID
}

// upsertFoods returns the food ID of every row that was written. A row absent
// from the result lost to a live row of higher confidence.
func upsertFoods(ctx context.Context, tx pgx.Tx, items []StagedItem) (map[string]int64, error) {
	n := len(items)
	kinds, names, brands, barcodes, states := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	kcal, protein, fat, carbs := make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	packages := make([]*float64, n)
	sources, urls, externalIDs, licenses, hashes := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	confidences := make([]int16, n)
	owners := make([]*int64, n)
	fetched := make([]*time.Time, n)
	for i, item := range items {
		f := item.Normalized
		kinds[i], names[i], brands[i], barcodes[i], states[i] = string(f.Kind), f.Name, f.Brand, f.Barcode, string(f.State)
		kcal[i], protein[i], fat[i], carbs[i] = f.Nutrition.Kcal, f.Nutrition.Protein, f.Nutrition.Fat, f.Nutrition.Carbs
		packages[i] = f.PackageG
		sources[i], urls[i], externalIDs[i], licenses[i], hashes[i] = item.SourceID, f.SourceURL, item.ExternalID, string(f.LicenseClass), item.ContentHash
		confidences[i] = int16(f.Confidence)
		owners[i] = f.OwnerUserID
		fetched[i] = f.FetchedAt
	}

	rows, err := tx.Query(ctx, `
		insert into foods (
			kind, name, brand, barcode, state, kcal_100g, protein_100g,
			fat_100g, carbs_100g, package_g, source_id, source_url,
			external_id, license_class, confidence, owner_user_id,
			content_hash, fetched_at
		)
		select kind, name, brand, nullif(barcode, ''), state, kcal, protein,
			fat, carbs, package_g, source_id, source_url, external_id,
			license_class, confidence, owner_user_id, content_hash, fetched_at
		from unnest(
			$1::text[], $2::text[], $3::text[], $4::text[], $5::text[],
			$6::float8[], $7::float8[], $8::float8[], $9::float8[], $10::float8[],
			$11::text[], $12::text[], $13::text[], $14::text[], $15::smallint[],
			$16::bigint[], $17::text[], $18::timestamptz[]
		) as t(kind, name, brand, barcode, state, kcal, protein, fat, carbs,
			package_g, source_id, source_url, external_id, license_class,
			confidence, owner_user_id, content_hash, fetched_at)
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
		returning id, source_id, external_id`,
		kinds, names, brands, barcodes, states, kcal, protein, fat, carbs,
		packages, sources, urls, externalIDs, licenses, confidences, owners,
		hashes, fetched)
	if err != nil {
		return nil, fmt.Errorf("upsert promoted foods: %w", err)
	}
	defer rows.Close()

	ids := make(map[string]int64, n)
	for rows.Next() {
		var id int64
		var sourceID, externalID string
		err = rows.Scan(&id, &sourceID, &externalID)
		if err != nil {
			return nil, fmt.Errorf("scan promoted food: %w", err)
		}
		ids[promoteKey(sourceID, externalID)] = id
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate promoted foods: %w", err)
	}
	return ids, nil
}

// recordConflicts fills in the live food ID of each conflicting row and queues
// one review item per conflict.
func (r *IngestRepo) recordConflicts(ctx context.Context, tx pgx.Tx, items []StagedItem, conflicts []int, results []PromoteResult) error {
	if len(conflicts) == 0 {
		return nil
	}
	sources := make([]string, len(conflicts))
	externalIDs := make([]string, len(conflicts))
	names := make([]string, len(conflicts))
	payloads := make([]string, len(conflicts))
	for i, index := range conflicts {
		item := items[index]
		sources[i], externalIDs[i], names[i] = item.SourceID, item.ExternalID, item.Normalized.Name
		payloads[i] = string(nullableJSON(item.Payload))
	}

	rows, err := tx.Query(ctx, `
		select f.id, f.source_id, f.external_id
		from foods f
		join unnest($1::text[], $2::text[]) as t(source_id, external_id)
			on f.source_id = t.source_id and f.external_id = t.external_id`, sources, externalIDs)
	if err != nil {
		return fmt.Errorf("get conflicting foods: %w", err)
	}
	live := make(map[string]int64, len(conflicts))
	for rows.Next() {
		var id int64
		var sourceID, externalID string
		err = rows.Scan(&id, &sourceID, &externalID)
		if err != nil {
			rows.Close()
			return fmt.Errorf("scan conflicting food: %w", err)
		}
		live[promoteKey(sourceID, externalID)] = id
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate conflicting foods: %w", err)
	}
	for _, index := range conflicts {
		item := items[index]
		results[index] = PromoteResult{FoodID: live[promoteKey(item.SourceID, item.ExternalID)], Conflict: true}
	}

	_, err = tx.Exec(ctx, `
		insert into review_queue (raw_input, input_type, reason, payload)
		select name, 'ingest', $1, payload::jsonb
		from unnest($2::text[], $3::text[]) as t(name, payload)`,
		domain.ReviewIngestConflict, names, payloads)
	if err != nil {
		return fmt.Errorf("queue ingest conflicts: %w", err)
	}
	return nil
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
