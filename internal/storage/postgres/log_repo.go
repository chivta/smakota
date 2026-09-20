package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LogRepo struct {
	pool *pgxpool.Pool
}

func NewLogRepo(pool *pgxpool.Pool) *LogRepo {
	return &LogRepo{pool: pool}
}

func (r *LogRepo) Add(ctx context.Context, e domain.LogEntry) (domain.LogEntry, error) {
	err := r.pool.QueryRow(ctx, `
		insert into log_entries (
			user_id, food_id, food_name, grams, kcal, protein, fat, carbs,
			logged_at, day
		) values ($1, nullif($2, 0), $3, $4, $5, $6, $7, $8, $9, $10)
		returning id, logged_at`,
		e.UserID, e.FoodID, e.FoodName, e.Grams, e.Nutrition.Kcal,
		e.Nutrition.Protein, e.Nutrition.Fat, e.Nutrition.Carbs, e.LoggedAt,
		dayString(e.Day),
	).Scan(&e.ID, &e.LoggedAt)
	if err != nil {
		return domain.LogEntry{}, fmt.Errorf("add log entry: %w", err)
	}
	return e, nil
}

func (r *LogRepo) Day(ctx context.Context, userID int64, day time.Time) ([]domain.LogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		select id, user_id, coalesce(food_id, 0), food_name, grams,
			kcal, protein, fat, carbs, logged_at, day
		from log_entries
		where user_id = $1 and day = $2
		order by logged_at, id`, userID, dayString(day))
	if err != nil {
		return nil, fmt.Errorf("list day log entries: %w", err)
	}
	defer rows.Close()

	entries := make([]domain.LogEntry, 0)
	for rows.Next() {
		e, scanErr := scanLogEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan day log entry: %w", scanErr)
		}
		entries = append(entries, e)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate day log entries: %w", err)
	}
	return entries, nil
}

func (r *LogRepo) UpdateGrams(ctx context.Context, userID, entryID int64, grams float64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin update log grams: %w", err)
	}
	defer tx.Rollback(ctx)

	locked, err := logEntryLocked(ctx, tx, userID, entryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check log entry edit window: %w", err)
	}
	if locked {
		return domain.ErrDayLocked
	}

	_, err = tx.Exec(ctx, `
		update log_entries set
			grams = $3,
			kcal = kcal / grams * $3,
			protein = protein / grams * $3,
			fat = fat / grams * $3,
			carbs = carbs / grams * $3
		where user_id = $1 and id = $2`, userID, entryID, grams)
	if err != nil {
		return fmt.Errorf("update log entry grams: %w", err)
	}
	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("commit log grams update: %w", err)
	}
	return nil
}

func (r *LogRepo) Delete(ctx context.Context, userID, entryID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete log entry: %w", err)
	}
	defer tx.Rollback(ctx)

	locked, err := logEntryLocked(ctx, tx, userID, entryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check log entry edit window: %w", err)
	}
	if locked {
		return domain.ErrDayLocked
	}

	_, err = tx.Exec(ctx, `delete from log_entries where user_id = $1 and id = $2`, userID, entryID)
	if err != nil {
		return fmt.Errorf("delete log entry: %w", err)
	}
	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("commit log entry delete: %w", err)
	}
	return nil
}

func (r *LogRepo) CopyDay(ctx context.Context, userID int64, from, to time.Time) (int, error) {
	result, err := r.pool.Exec(ctx, `
		insert into log_entries (
			user_id, food_id, food_name, grams, kcal, protein, fat, carbs,
			logged_at, day
		)
		select le.user_id, le.food_id, le.food_name, le.grams, le.kcal,
			le.protein, le.fat, le.carbs,
			($3::date + (le.logged_at at time zone u.timezone)::time) at time zone u.timezone,
			$3::date
		from log_entries le
		join users u on u.id = le.user_id
		where le.user_id = $1 and le.day = $2::date
		order by le.logged_at, le.id`, userID, dayString(from), dayString(to))
	if err != nil {
		return 0, fmt.Errorf("copy log day: %w", err)
	}
	return int(result.RowsAffected()), nil
}

func scanLogEntry(row pgx.Row) (domain.LogEntry, error) {
	var e domain.LogEntry
	err := row.Scan(
		&e.ID, &e.UserID, &e.FoodID, &e.FoodName, &e.Grams,
		&e.Nutrition.Kcal, &e.Nutrition.Protein, &e.Nutrition.Fat,
		&e.Nutrition.Carbs, &e.LoggedAt, &e.Day,
	)
	return e, err
}

func logEntryLocked(ctx context.Context, tx pgx.Tx, userID, entryID int64) (bool, error) {
	var locked bool
	err := tx.QueryRow(ctx, `
		select le.day < (now() at time zone u.timezone)::date - $3::int
		from log_entries le
		join users u on u.id = le.user_id
		where le.user_id = $1 and le.id = $2
		for update of le`,
		userID, entryID, int(domain.DayEditWindow/(24*time.Hour)),
	).Scan(&locked)
	return locked, err
}

func dayString(day time.Time) string {
	return day.Format(time.DateOnly)
}
