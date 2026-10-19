package postgres

import (
	"context"
	"fmt"
)

// ReindexProgress is the state after one batch of ReindexAliases.
type ReindexProgress struct {
	LastID    int64
	Scanned   int
	Rewritten int
	Removed   int
}

// ReindexAliases recomputes food_aliases.norm for every row after afterID with
// the repo's normalizer, rewriting only rows whose norm differs. A row whose
// new norm is empty or already held by another alias of the same food is
// deleted, since the unique (food_id, norm) index would reject it. It is safe
// to rerun and to resume from the last reported ID.
func (r *FoodRepo) ReindexAliases(ctx context.Context, afterID int64, batchSize int, report func(ReindexProgress)) (ReindexProgress, error) {
	total := ReindexProgress{LastID: afterID}
	for {
		batch, err := r.reindexAliasBatch(ctx, total.LastID, batchSize)
		if err != nil {
			return total, err
		}
		if batch.Scanned == 0 {
			return total, nil
		}
		total.LastID = batch.LastID
		total.Scanned += batch.Scanned
		total.Rewritten += batch.Rewritten
		total.Removed += batch.Removed
		report(total)
	}
}

func (r *FoodRepo) reindexAliasBatch(ctx context.Context, afterID int64, batchSize int) (ReindexProgress, error) {
	rows, err := r.pool.Query(ctx, `
		select id, food_id, alias, norm from food_aliases
		where id > $1 order by id limit $2`, afterID, batchSize)
	if err != nil {
		return ReindexProgress{}, fmt.Errorf("read aliases: %w", err)
	}
	defer rows.Close()

	batch := ReindexProgress{LastID: afterID}
	type foodNorm struct {
		foodID int64
		norm   string
	}
	keeper := make(map[foodNorm]int64)
	var changedIDs, changedFoods []int64
	var changedNorms []string
	var dropIDs []int64
	for rows.Next() {
		var id, foodID int64
		var alias, stored string
		err = rows.Scan(&id, &foodID, &alias, &stored)
		if err != nil {
			return ReindexProgress{}, fmt.Errorf("scan alias: %w", err)
		}
		batch.Scanned++
		batch.LastID = id

		norm := r.normalize(alias)
		if norm == stored {
			continue
		}
		changedIDs = append(changedIDs, id)
		changedFoods = append(changedFoods, foodID)
		changedNorms = append(changedNorms, norm)
	}
	err = rows.Err()
	if err != nil {
		return ReindexProgress{}, fmt.Errorf("read aliases: %w", err)
	}

	// Rows arrive in id order, so the last of a colliding group wins, as in
	// the write path.
	for i, id := range changedIDs {
		keeper[foodNorm{changedFoods[i], changedNorms[i]}] = id
	}
	var keepIDs []int64
	var keepNorms []string
	for i, id := range changedIDs {
		if changedNorms[i] == "" || keeper[foodNorm{changedFoods[i], changedNorms[i]}] != id {
			dropIDs = append(dropIDs, id)
			continue
		}
		keepIDs = append(keepIDs, id)
		keepNorms = append(keepNorms, changedNorms[i])
	}
	if len(changedIDs) == 0 {
		return batch, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ReindexProgress{}, fmt.Errorf("begin reindex transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `delete from food_aliases where id = any($1)`, dropIDs)
	if err != nil {
		return ReindexProgress{}, fmt.Errorf("delete duplicate aliases: %w", err)
	}
	batch.Removed += int(tag.RowsAffected())

	// Rows whose new norm another alias of the food already holds.
	tag, err = tx.Exec(ctx, `
		delete from food_aliases a
		using unnest($1::bigint[], $2::text[]) as t(id, norm)
		where a.id = t.id and exists (
			select 1 from food_aliases o
			where o.food_id = a.food_id and o.norm = t.norm and o.id <> a.id)`,
		keepIDs, keepNorms)
	if err != nil {
		return ReindexProgress{}, fmt.Errorf("delete colliding aliases: %w", err)
	}
	batch.Removed += int(tag.RowsAffected())

	tag, err = tx.Exec(ctx, `
		update food_aliases a set norm = t.norm
		from unnest($1::bigint[], $2::text[]) as t(id, norm)
		where a.id = t.id`, keepIDs, keepNorms)
	if err != nil {
		return ReindexProgress{}, fmt.Errorf("rewrite alias norms: %w", err)
	}
	batch.Rewritten = int(tag.RowsAffected())

	err = tx.Commit(ctx)
	if err != nil {
		return ReindexProgress{}, fmt.Errorf("commit reindex batch: %w", err)
	}
	return batch, nil
}
