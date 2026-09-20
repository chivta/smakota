package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const foodColumns = `
	f.id, f.kind, f.name, f.brand, coalesce(f.barcode, ''), f.state,
	f.kcal_100g, f.protein_100g, f.fat_100g, f.carbs_100g, f.package_g,
	f.source_id, f.source_url, f.license_class, f.confidence,
	f.owner_user_id, f.fetched_at`

type SearchParams struct {
	Query  string
	UserID *int64
	Limit  int
}

type Candidate struct {
	Food  domain.Food `json:"food"`
	Score float64     `json:"score"`
}

type RecentFood struct {
	Food      domain.Food `json:"food"`
	Count     int         `json:"count"`
	LastGrams float64     `json:"last_grams"`
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
	rows, err := r.pool.Query(ctx, `
		select `+foodColumns+`, max(similarity(fa.norm, $1)) as score
		from food_aliases fa
		join foods f on f.id = fa.food_id
		where fa.norm % $1
			and (f.owner_user_id is null or f.owner_user_id = $2)
		group by f.id
		order by score desc,
			case when f.owner_user_id = $2 then 1 else 0 end desc,
			f.confidence desc,
			f.id
		limit $3`, p.Query, p.UserID, p.Limit)
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

func (r *FoodRepo) Recents(ctx context.Context, userID int64, days, limit int) ([]RecentFood, error) {
	rows, err := r.pool.Query(ctx, `
		select `+foodColumns+`, count(*)::int,
			(array_agg(le.grams order by le.logged_at desc))[1]
		from log_entries le
		join users u on u.id = le.user_id
		join foods f on f.id = le.food_id
		where le.user_id = $1
			and le.day >= (now() at time zone u.timezone)::date - greatest($2 - 1, 0)
		group by f.id
		order by count(*) desc, max(le.logged_at) desc, f.id
		`, userID, days)
	if err != nil {
		return nil, fmt.Errorf("list recent foods: %w", err)
	}
	defer rows.Close()

	recents := make([]RecentFood, 0, limit)
	for rows.Next() {
		var recent RecentFood
		food, scanErr := scanFoodWithRecent(rows, &recent.Count, &recent.LastGrams)
		if scanErr != nil {
			return nil, fmt.Errorf("scan recent food: %w", scanErr)
		}
		personalToUser := food.LicenseClass == domain.LicenseUser &&
			food.OwnerUserID != nil && *food.OwnerUserID == userID
		if !food.LicenseClass.Exportable() && !personalToUser {
			continue
		}
		recent.Food = food
		recents = append(recents, recent)
		if len(recents) == limit {
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent foods: %w", err)
	}
	rows.Close()
	for i := range recents {
		err = r.loadPortions(ctx, &recents[i].Food)
		if err != nil {
			return nil, err
		}
	}
	return recents, nil
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

func (r *FoodRepo) LastAmount(ctx context.Context, userID, foodID int64) (float64, bool, error) {
	var grams float64
	err := r.pool.QueryRow(ctx, `
		select grams from log_entries
		where user_id = $1 and food_id = $2
		order by logged_at desc, id desc
		limit 1`, userID, foodID).Scan(&grams)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get last food amount: %w", err)
	}
	return grams, true, nil
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
	f := &candidate.Food
	err := row.Scan(
		&f.ID, &f.Kind, &f.Name, &f.Brand, &f.Barcode, &f.State,
		&f.Nutrition.Kcal, &f.Nutrition.Protein, &f.Nutrition.Fat,
		&f.Nutrition.Carbs, &f.PackageG, &f.SourceID, &f.SourceURL,
		&f.LicenseClass, &f.Confidence, &f.OwnerUserID, &f.FetchedAt,
		&candidate.Score,
	)
	return candidate, err
}

func scanFoodWithRecent(row pgx.Row, count *int, lastGrams *float64) (domain.Food, error) {
	var f domain.Food
	err := row.Scan(
		&f.ID, &f.Kind, &f.Name, &f.Brand, &f.Barcode, &f.State,
		&f.Nutrition.Kcal, &f.Nutrition.Protein, &f.Nutrition.Fat,
		&f.Nutrition.Carbs, &f.PackageG, &f.SourceID, &f.SourceURL,
		&f.LicenseClass, &f.Confidence, &f.OwnerUserID, &f.FetchedAt,
		count, lastGrams,
	)
	return f, err
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
