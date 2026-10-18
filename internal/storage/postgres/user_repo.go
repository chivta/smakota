package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const userColumns = `
	u.id, coalesce(u.sex, ''), coalesce(u.age, 0),
	coalesce(u.height_cm, 0), coalesce(u.weight_kg, 0),
	coalesce(u.goal_weight_kg, 0), coalesce(u.activity_level, ''),
	coalesce(u.kcal_target, 0), coalesce(u.protein_target_g, 0),
	u.timezone, u.onboarded_at`

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `select `+userColumns+` from users u where u.id = $1`, id)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// Create inserts an empty profile; every column has a default.
func (r *UserRepo) Create(ctx context.Context) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `insert into users as u default values returning `+userColumns)
	created, err := scanUser(row)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (r *UserRepo) Update(ctx context.Context, u domain.User) error {
	err := r.pool.QueryRow(ctx, `
		update users set
			sex = nullif($2, ''),
			age = nullif($3, 0),
			height_cm = nullif($4, 0),
			weight_kg = nullif($5, 0),
			goal_weight_kg = nullif($6, 0),
			activity_level = nullif($7, ''),
			kcal_target = nullif($8, 0),
			protein_target_g = nullif($9, 0),
			timezone = $10,
			onboarded_at = $11
		where id = $1
		returning id`,
		u.ID, u.Sex, u.Age, u.HeightCM, u.WeightKG,
		u.GoalWeightKG, u.ActivityLevel, u.KcalTarget, u.ProteinTargetG,
		u.Timezone, u.OnboardedAt,
	).Scan(&u.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(
		&u.ID, &u.Sex, &u.Age, &u.HeightCM, &u.WeightKG,
		&u.GoalWeightKG, &u.ActivityLevel, &u.KcalTarget, &u.ProteinTargetG,
		&u.Timezone, &u.OnboardedAt,
	)
	return u, err
}
