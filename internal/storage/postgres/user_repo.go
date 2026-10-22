package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation is the SQLSTATE of a unique index conflict.
const uniqueViolation = "23505"

const userColumns = `
	u.id, coalesce(u.email, ''), coalesce(u.sex, ''), coalesce(u.age, 0),
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

// CreateWithCredentials inserts a user that can log in. The email must already
// be normalized; a taken one is ErrConflict.
func (r *UserRepo) CreateWithCredentials(ctx context.Context, email, passwordHash string) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `
		insert into users as u (email, password_hash) values ($1, $2)
		returning `+userColumns,
		email, passwordHash)
	created, err := scanUser(row)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return domain.User{}, domain.ErrConflict
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("create user with credentials: %w", err)
	}
	return created, nil
}

// GetCredentials returns the id and password hash of the user with this
// normalized email, or ErrNotFound.
func (r *UserRepo) GetCredentials(ctx context.Context, email string) (int64, string, error) {
	var id int64
	var hash string
	err := r.pool.QueryRow(ctx, `
		select id, password_hash from users
		where lower(email) = $1 and password_hash is not null`,
		email,
	).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", domain.ErrNotFound
	}
	if err != nil {
		return 0, "", fmt.Errorf("get credentials: %w", err)
	}
	return id, hash, nil
}

// GetOrCreateBySubject returns the user bound to an identity provider subject,
// creating it on first sight. The email is stored only when no other user has
// it; an account is never linked to an existing one by email.
func (r *UserRepo) GetOrCreateBySubject(ctx context.Context, subject, email string) (int64, error) {
	_, err := r.pool.Exec(ctx, `
		insert into users (subject, email)
		values ($1, case
			when exists (select 1 from users where lower(email) = $2) then null
			else nullif($2, '')
		end)
		on conflict (subject) do nothing`,
		subject, email)
	if err != nil {
		return 0, fmt.Errorf("create user by subject: %w", err)
	}

	var id int64
	err = r.pool.QueryRow(ctx, `select id from users where subject = $1`, subject).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("get user by subject: %w", err)
	}
	return id, nil
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
		&u.ID, &u.Email, &u.Sex, &u.Age, &u.HeightCM, &u.WeightKG,
		&u.GoalWeightKG, &u.ActivityLevel, &u.KcalTarget, &u.ProteinTargetG,
		&u.Timezone, &u.OnboardedAt,
	)
	return u, err
}
