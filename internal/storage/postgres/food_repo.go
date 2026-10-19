package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const foodColumns = `
	f.id, f.kind, f.name, f.brand, coalesce(f.barcode, ''), f.state,
	f.kcal_100g, f.protein_100g, f.fat_100g, f.carbs_100g, f.package_g,
	f.source_id, f.source_url, f.license_class, f.confidence,
	f.owner_user_id, f.fetched_at`

// Starting values for the history bonus, to be tuned against real data.
const (
	AffinityWeight      = 0.25
	FamiliarAtLogs      = 10
	RecencyHalfLifeDays = 30
	RecencyFloor        = 0.3
)

// affinityBonus is the additive ranking bonus on the 0..1 similarity scale.
// It is capped at AffinityWeight, so history nudges ties but cannot lift a
// poor lexical match above a good one. The search SQL mirrors this formula.
func affinityBonus(logs int, daysSinceLast float64) float64 {
	if logs <= 0 {
		return 0
	}
	familiarity := math.Min(math.Log1p(float64(logs))/math.Log1p(FamiliarAtLogs), 1)
	recency := math.Exp(-daysSinceLast / RecencyHalfLifeDays)
	return AffinityWeight * familiarity * (RecencyFloor + (1-RecencyFloor)*recency)
}

// bonusSQL is affinityBonus over the history CTE's columns h.logs and
// h.last_logged; $4..$7 are the four constants above.
const bonusSQL = `coalesce(
	$4::float8
		* least(ln(1 + h.logs) / ln(1 + $5::float8), 1)
		* ($7::float8 + (1 - $7::float8)
			* exp(-extract(epoch from now() - h.last_logged) / 86400 / $6::float8)),
	0)`

type SearchParams struct {
	Query  string
	UserID *int64
	Limit  int
	Offset int
}

// Plain similarity divides by the candidate's length, so a short query such as
// "ріс" scores below the threshold against "ріс білій сухій" and returns
// nothing. strict_word_similarity compares the query against word-bounded
// substrings instead, which finds the word however long the rest of the name
// is. Taking the greater of the two keeps full-phrase matches ranked first,
// and both operators read the same trigram index.

// Candidate is one match. Score is the lexical match strength; Bonus is the
// caller's history affinity added on top of it when ranking.
type Candidate struct {
	Food  domain.Food `json:"food"`
	Score float64     `json:"score"`
	Bonus float64     `json:"bonus"`
}

type FoodRepo struct {
	pool *pgxpool.Pool
}

func NewFoodRepo(pool *pgxpool.Pool) *FoodRepo {
	return &FoodRepo{pool: pool}
}

func (r *FoodRepo) GetByID(ctx context.Context, id int64) (domain.Food, error) {
	row := r.pool.QueryRow(ctx, `select `+foodColumns+` from foods f where f.id = $1`, id)
	f, err := scanFood(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Food{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Food{}, fmt.Errorf("get food by id: %w", err)
	}

	err = r.loadPortions(ctx, &f)
	if err != nil {
		return domain.Food{}, err
	}
	return f, nil
}

func (r *FoodRepo) GetByBarcode(ctx context.Context, barcode string) (domain.Food, error) {
	row := r.pool.QueryRow(ctx, `
		select `+foodColumns+`
		from foods f
		where f.barcode = $1 and f.owner_user_id is null
		order by f.confidence desc, f.updated_at desc
		limit 1`, barcode)
	f, err := scanFood(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Food{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Food{}, fmt.Errorf("get food by barcode: %w", err)
	}

	err = r.loadPortions(ctx, &f)
	if err != nil {
		return domain.Food{}, err
	}
	return f, nil
}

func (r *FoodRepo) Search(ctx context.Context, p SearchParams) ([]Candidate, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if p.UserID == nil || *p.UserID == 0 {
		rows, err = r.pool.Query(ctx, `
			select `+foodColumns+`, max(greatest(similarity(fa.norm, $1), strict_word_similarity($1, fa.norm))) as score,
				max(similarity(fa.norm, $1)) as tightness, 0::float8 as bonus
			from food_aliases fa
			join foods f on f.id = fa.food_id
			where (fa.norm % $1 or $1 <<% fa.norm)
				and f.owner_user_id is null
			group by f.id
			order by score desc,
				tightness desc,
				f.confidence desc,
				f.id
			limit $2 offset $3`, p.Query, p.Limit, p.Offset)
	} else {
		rows, err = r.pool.Query(ctx, `
			with history as (
				select food_id, count(*) as logs, max(logged_at) as last_logged
				from log_entries
				where user_id = $2 and food_id is not null
				group by food_id
			)
			select * from (
				select `+foodColumns+`, max(greatest(similarity(fa.norm, $1), strict_word_similarity($1, fa.norm))) as score,
					max(similarity(fa.norm, $1)) as tightness,
					`+bonusSQL+` as bonus
				from food_aliases fa
				join foods f on f.id = fa.food_id
				left join history h on h.food_id = f.id
				where (fa.norm % $1 or $1 <<% fa.norm)
					and (f.owner_user_id is null or f.owner_user_id = $2)
				group by f.id, h.logs, h.last_logged
			) ranked
			order by score + bonus desc,
				tightness desc,
				case when owner_user_id = $2 then 1 else 0 end desc,
				confidence desc,
				id
			limit $3 offset $8`, p.Query, *p.UserID, p.Limit,
			float64(AffinityWeight), float64(FamiliarAtLogs), float64(RecencyHalfLifeDays), float64(RecencyFloor), p.Offset)
	}
	if err != nil {
		return nil, fmt.Errorf("search foods: %w", err)
	}
	defer rows.Close()

	candidates := make([]Candidate, 0)
	for rows.Next() {
		candidate, scanErr := scanFoodWithScore(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan food candidate: %w", scanErr)
		}
		candidates = append(candidates, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate food candidates: %w", err)
	}
	rows.Close()
	for i := range candidates {
		err = r.loadPortions(ctx, &candidates[i].Food)
		if err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func (r *FoodRepo) CreatePersonal(ctx context.Context, userID int64, f domain.Food) (domain.Food, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Food{}, fmt.Errorf("begin create personal food: %w", err)
	}
	defer tx.Rollback(ctx)

	f.OwnerUserID = &userID
	f.LicenseClass = domain.LicenseUser
	f.Confidence = domain.ConfidenceUser
	if f.SourceID == "" {
		f.SourceID = "user"
	}
	err = tx.QueryRow(ctx, `
		insert into foods (
			kind, name, brand, barcode, state, kcal_100g, protein_100g,
			fat_100g, carbs_100g, package_g, source_id, source_url,
			license_class, confidence, owner_user_id, fetched_at
		) values (
			$1, $2, $3, nullif($4, ''), $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16
		)
		returning id`,
		f.Kind, f.Name, f.Brand, f.Barcode, f.State, f.Nutrition.Kcal,
		f.Nutrition.Protein, f.Nutrition.Fat, f.Nutrition.Carbs, f.PackageG,
		f.SourceID, f.SourceURL, f.LicenseClass, f.Confidence, f.OwnerUserID,
		f.FetchedAt,
	).Scan(&f.ID)
	if err != nil {
		return domain.Food{}, fmt.Errorf("insert personal food: %w", err)
	}

	err = replaceFoodDetails(ctx, tx, f)
	if err != nil {
		return domain.Food{}, fmt.Errorf("save personal food details: %w", err)
	}
	err = tx.Commit(ctx)
	if err != nil {
		return domain.Food{}, fmt.Errorf("commit personal food: %w", err)
	}
	return f, nil
}

func (r *FoodRepo) loadPortions(ctx context.Context, f *domain.Food) error {
	rows, err := r.pool.Query(ctx, `
		select id, food_id, name, grams, is_default
		from food_portions
		where food_id = $1
		order by is_default desc, id`, f.ID)
	if err != nil {
		return fmt.Errorf("list food portions: %w", err)
	}
	defer rows.Close()

	f.Portions = make([]domain.Portion, 0)
	for rows.Next() {
		var portion domain.Portion
		err = rows.Scan(&portion.ID, &portion.FoodID, &portion.Name, &portion.Grams, &portion.IsDefault)
		if err != nil {
			return fmt.Errorf("scan food portion: %w", err)
		}
		f.Portions = append(f.Portions, portion)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate food portions: %w", err)
	}
	return nil
}

func scanFood(row pgx.Row) (domain.Food, error) {
	var f domain.Food
	err := row.Scan(
		&f.ID, &f.Kind, &f.Name, &f.Brand, &f.Barcode, &f.State,
		&f.Nutrition.Kcal, &f.Nutrition.Protein, &f.Nutrition.Fat,
		&f.Nutrition.Carbs, &f.PackageG, &f.SourceID, &f.SourceURL,
		&f.LicenseClass, &f.Confidence, &f.OwnerUserID, &f.FetchedAt,
	)
	return f, err
}

func scanFoodWithScore(row pgx.Row) (Candidate, error) {
	var candidate Candidate
	// tightness only orders the results; callers never see it.
	var tightness float64
	f := &candidate.Food
	err := row.Scan(
		&f.ID, &f.Kind, &f.Name, &f.Brand, &f.Barcode, &f.State,
		&f.Nutrition.Kcal, &f.Nutrition.Protein, &f.Nutrition.Fat,
		&f.Nutrition.Carbs, &f.PackageG, &f.SourceID, &f.SourceURL,
		&f.LicenseClass, &f.Confidence, &f.OwnerUserID, &f.FetchedAt,
		&candidate.Score, &tightness, &candidate.Bonus,
	)
	return candidate, err
}

func replaceFoodDetails(ctx context.Context, tx pgx.Tx, f domain.Food) error {
	_, err := tx.Exec(ctx, `delete from food_portions where food_id = $1`, f.ID)
	if err != nil {
		return fmt.Errorf("delete food portions: %w", err)
	}
	for _, portion := range f.Portions {
		_, err = tx.Exec(ctx, `
			insert into food_portions (food_id, name, grams, is_default)
			values ($1, $2, $3, $4)`, f.ID, portion.Name, portion.Grams, portion.IsDefault)
		if err != nil {
			return fmt.Errorf("insert food portion: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `delete from food_aliases where food_id = $1`, f.ID)
	if err != nil {
		return fmt.Errorf("delete food aliases: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into food_aliases (food_id, alias, norm, lang)
		values ($1, $2, lower(unaccent($2)), 'uk')`, f.ID, f.Name)
	if err != nil {
		return fmt.Errorf("insert food name alias: %w", err)
	}
	for _, alias := range f.Aliases {
		_, err = tx.Exec(ctx, `
			insert into food_aliases (food_id, alias, norm, lang)
			values ($1, $2, lower(unaccent($2)), $3)
			on conflict (food_id, norm) do update
			set alias = excluded.alias, lang = excluded.lang`,
			f.ID, alias.Alias, alias.Lang)
		if err != nil {
			return fmt.Errorf("insert food alias: %w", err)
		}
	}
	return nil
}

// replaceFoodDetailsBatch is replaceFoodDetails for many foods in a fixed
// number of statements. Aliases that fold to the same norm collapse to the
// last one listed, as the row-by-row upsert did.
func replaceFoodDetailsBatch(ctx context.Context, tx pgx.Tx, foods []domain.Food) error {
	if len(foods) == 0 {
		return nil
	}
	foodIDs := make([]int64, len(foods))
	var portionFoods []int64
	var portionNames []string
	var portionGrams []float64
	var portionDefaults []bool
	var aliasFoods []int64
	var aliasTexts, aliasLangs []string
	for i, f := range foods {
		foodIDs[i] = f.ID
		for _, portion := range f.Portions {
			portionFoods = append(portionFoods, f.ID)
			portionNames = append(portionNames, portion.Name)
			portionGrams = append(portionGrams, portion.Grams)
			portionDefaults = append(portionDefaults, portion.IsDefault)
		}
		aliasFoods = append(aliasFoods, f.ID)
		aliasTexts = append(aliasTexts, f.Name)
		aliasLangs = append(aliasLangs, "uk")
		for _, alias := range f.Aliases {
			aliasFoods = append(aliasFoods, f.ID)
			aliasTexts = append(aliasTexts, alias.Alias)
			aliasLangs = append(aliasLangs, alias.Lang)
		}
	}

	_, err := tx.Exec(ctx, `delete from food_portions where food_id = any($1)`, foodIDs)
	if err != nil {
		return fmt.Errorf("delete food portions: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into food_portions (food_id, name, grams, is_default)
		select * from unnest($1::bigint[], $2::text[], $3::float8[], $4::bool[])`,
		portionFoods, portionNames, portionGrams, portionDefaults)
	if err != nil {
		return fmt.Errorf("insert food portions: %w", err)
	}
	_, err = tx.Exec(ctx, `delete from food_aliases where food_id = any($1)`, foodIDs)
	if err != nil {
		return fmt.Errorf("delete food aliases: %w", err)
	}
	_, err = tx.Exec(ctx, `
		insert into food_aliases (food_id, alias, norm, lang)
		select distinct on (food_id, lower(unaccent(alias)))
			food_id, alias, lower(unaccent(alias)), lang
		from unnest($1::bigint[], $2::text[], $3::text[]) with ordinality
			as t(food_id, alias, lang, ord)
		order by food_id, lower(unaccent(alias)), ord desc`,
		aliasFoods, aliasTexts, aliasLangs)
	if err != nil {
		return fmt.Errorf("insert food aliases: %w", err)
	}
	return nil
}
