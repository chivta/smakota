package postgres

import (
	"context"
	"fmt"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/jackc/pgx/v5"
)

// SeedCatalogue writes the curated entries, their portions and their aliases in
// one transaction. It is re-runnable: entries are keyed by (source_id,
// external_id), so a second run updates rather than duplicates.
//
// normalize must be the same function the resolver applies to a user's query.
// The alias norm column is the join between the two, so a mismatch here makes
// every curated entry unfindable.
func (r *FoodRepo) SeedCatalogue(ctx context.Context, foods []domain.Food, normalize func(string) string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	written := 0
	for _, food := range foods {
		id, err := upsertFood(ctx, tx, food)
		if err != nil {
			return written, err
		}

		err = replacePortions(ctx, tx, id, food.Portions)
		if err != nil {
			return written, err
		}

		err = replaceAliases(ctx, tx, id, food, normalize)
		if err != nil {
			return written, err
		}
		written++
	}

	err = tx.Commit(ctx)
	if err != nil {
		return written, fmt.Errorf("commit seed transaction: %w", err)
	}
	return written, nil
}

const upsertFoodSQL = `
insert into foods (kind, name, brand, state, kcal_100g, protein_100g, fat_100g, carbs_100g,
                   source_id, external_id, license_class, confidence, fetched_at)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
on conflict (source_id, external_id) where external_id is not null
do update set name = excluded.name,
              kind = excluded.kind,
              state = excluded.state,
              kcal_100g = excluded.kcal_100g,
              protein_100g = excluded.protein_100g,
              fat_100g = excluded.fat_100g,
              carbs_100g = excluded.carbs_100g,
              confidence = excluded.confidence
returning id`

// upsertFood keys the curated entry by its name, which is stable across runs
// and unique within the catalogue.
func upsertFood(ctx context.Context, tx pgx.Tx, f domain.Food) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, upsertFoodSQL,
		f.Kind, f.Name, f.Brand, f.State,
		f.Nutrition.Kcal, f.Nutrition.Protein, f.Nutrition.Fat, f.Nutrition.Carbs,
		f.SourceID, f.Name, f.LicenseClass, f.Confidence,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert food %s: %w", f.Name, err)
	}
	return id, nil
}

func replacePortions(ctx context.Context, tx pgx.Tx, foodID int64, portions []domain.Portion) error {
	_, err := tx.Exec(ctx, "delete from food_portions where food_id = $1", foodID)
	if err != nil {
		return fmt.Errorf("clear portions for %d: %w", foodID, err)
	}

	for _, p := range portions {
		_, err = tx.Exec(ctx,
			"insert into food_portions (food_id, name, grams, is_default) values ($1, $2, $3, $4)",
			foodID, p.Name, p.Grams, p.IsDefault)
		if err != nil {
			return fmt.Errorf("insert portion %s for %d: %w", p.Name, foodID, err)
		}
	}
	return nil
}

// replaceAliases writes every spelling plus the entry's own name, so a user who
// types the canonical name matches without the catalogue having to repeat it.
func replaceAliases(ctx context.Context, tx pgx.Tx, foodID int64, f domain.Food, normalize func(string) string) error {
	_, err := tx.Exec(ctx, "delete from food_aliases where food_id = $1", foodID)
	if err != nil {
		return fmt.Errorf("clear aliases for %d: %w", foodID, err)
	}

	seen := make(map[string]bool)
	aliases := append([]domain.Alias{{Alias: f.Name, Lang: "uk"}}, f.Aliases...)

	for _, a := range aliases {
		norm := normalize(a.Alias)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true

		_, err = tx.Exec(ctx,
			"insert into food_aliases (food_id, alias, norm, lang) values ($1, $2, $3, $4)",
			foodID, a.Alias, norm, a.Lang)
		if err != nil {
			return fmt.Errorf("insert alias %s for %d: %w", a.Alias, foodID, err)
		}
	}
	return nil
}
