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

const userColumns = `
	u.id, u.telegram_id, coalesce(u.sex, ''), coalesce(u.age, 0),
	coalesce(u.height_cm, 0), coalesce(u.weight_kg, 0),
	coalesce(u.goal_weight_kg, 0), coalesce(u.activity_level, ''),
	coalesce(u.kcal_target, 0), coalesce(u.protein_target_g, 0),
	u.timezone, to_char(u.nudge_at, 'HH24:MI'), u.nudge_enabled,
	u.weekly_summary_enabled, u.onboarded_at`

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

func (r *UserRepo) GetByTelegramID(ctx context.Context, tgID int64) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `select `+userColumns+` from users u where u.telegram_id = $1`, tgID)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user by telegram id: %w", err)
	}
	return u, nil
}

func (r *UserRepo) Create(ctx context.Context, u domain.User) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `
		insert into users as u (
			telegram_id, sex, age, height_cm, weight_kg, goal_weight_kg,
			activity_level, kcal_target, protein_target_g, timezone, nudge_at,
			nudge_enabled, weekly_summary_enabled, onboarded_at
		) values (
			$1, nullif($2, ''), nullif($3, 0), nullif($4, 0), nullif($5, 0),
			nullif($6, 0), nullif($7, ''), nullif($8, 0), nullif($9, 0),
			$10, $11::time, $12, $13, $14
		)
		returning `+userColumns,
		u.TelegramID, u.Sex, u.Age, u.HeightCM, u.WeightKG, u.GoalWeightKG,
		u.ActivityLevel, u.KcalTarget, u.ProteinTargetG, u.Timezone, u.NudgeAt,
		u.NudgeEnabled, u.WeeklySummaryEnabled, u.OnboardedAt,
	)
	created, err := scanUser(row)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (r *UserRepo) Update(ctx context.Context, u domain.User) error {
	err := r.pool.QueryRow(ctx, `
		update users set
			telegram_id = $2,
			sex = nullif($3, ''),
			age = nullif($4, 0),
			height_cm = nullif($5, 0),
			weight_kg = nullif($6, 0),
			goal_weight_kg = nullif($7, 0),
			activity_level = nullif($8, ''),
			kcal_target = nullif($9, 0),
			protein_target_g = nullif($10, 0),
			timezone = $11,
			nudge_at = $12::time,
			nudge_enabled = $13,
			weekly_summary_enabled = $14,
			onboarded_at = $15
		where id = $1
		returning id`,
		u.ID, u.TelegramID, u.Sex, u.Age, u.HeightCM, u.WeightKG,
		u.GoalWeightKG, u.ActivityLevel, u.KcalTarget, u.ProteinTargetG,
		u.Timezone, u.NudgeAt, u.NudgeEnabled, u.WeeklySummaryEnabled,
		u.OnboardedAt,
	).Scan(&u.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

func (r *UserRepo) ListDueForNudge(ctx context.Context, now time.Time) ([]domain.User, error) {
	rows, err := r.pool.Query(ctx, `
		select `+userColumns+`
		from users u
		cross join lateral (
			select $1::timestamptz at time zone u.timezone as local_now
		) local_time
		cross join lateral (
			values
				(local_time.local_now::date + u.nudge_at),
				(local_time.local_now::date - 1 + u.nudge_at)
		) scheduled(due_at)
		where u.nudge_enabled
			and scheduled.due_at > local_time.local_now - interval '10 minutes'
			and scheduled.due_at <= local_time.local_now
			and not exists (
				select 1 from log_entries le
				where le.user_id = u.id and le.logged_at >= local_time.local_now::date at time zone u.timezone
					and le.logged_at < (local_time.local_now::date + 1) at time zone u.timezone
			)
		order by u.id`, now)
	if err != nil {
		return nil, fmt.Errorf("list users due for nudge: %w", err)
	}
	defer rows.Close()

	users := make([]domain.User, 0)
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan user due for nudge: %w", scanErr)
		}
		users = append(users, u)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users due for nudge: %w", err)
	}
	return users, nil
}

func (r *UserRepo) ListDueForWeeklySummary(ctx context.Context, now time.Time) ([]domain.User, error) {
	rows, err := r.pool.Query(ctx, `
		select `+userColumns+`
		from users u
		cross join lateral (
			select $1::timestamptz at time zone u.timezone as local_now
		) local_time
		cross join lateral (
			values
				(local_time.local_now::date + u.nudge_at),
				(local_time.local_now::date - 1 + u.nudge_at)
		) scheduled(due_at)
		where u.weekly_summary_enabled
			and extract(isodow from scheduled.due_at) = 7
			and scheduled.due_at > local_time.local_now - interval '10 minutes'
			and scheduled.due_at <= local_time.local_now
		order by u.id`, now)
	if err != nil {
		return nil, fmt.Errorf("list users due for weekly summary: %w", err)
	}
	defer rows.Close()

	users := make([]domain.User, 0)
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan user due for weekly summary: %w", scanErr)
		}
		users = append(users, u)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users due for weekly summary: %w", err)
	}
	return users, nil
}

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(
		&u.ID, &u.TelegramID, &u.Sex, &u.Age, &u.HeightCM, &u.WeightKG,
		&u.GoalWeightKG, &u.ActivityLevel, &u.KcalTarget, &u.ProteinTargetG,
		&u.Timezone, &u.NudgeAt, &u.NudgeEnabled, &u.WeeklySummaryEnabled,
		&u.OnboardedAt,
	)
	return u, err
}
