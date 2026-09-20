package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReviewRepo struct {
	pool *pgxpool.Pool
}

func NewReviewRepo(pool *pgxpool.Pool) *ReviewRepo {
	return &ReviewRepo{pool: pool}
}

func (r *ReviewRepo) Add(ctx context.Context, item domain.ReviewItem) error {
	_, err := r.pool.Exec(ctx, `
		insert into review_queue (
			user_id, raw_input, input_type, reason, payload, resolved
		) values ($1, $2, $3, $4, $5, $6)`,
		item.UserID, item.RawInput, item.InputType, item.Reason,
		nullableJSON(item.Payload), item.Resolved)
	if err != nil {
		return fmt.Errorf("add review item: %w", err)
	}
	return nil
}

func (r *ReviewRepo) ListOpen(ctx context.Context, limit int) ([]domain.ReviewItem, error) {
	rows, err := r.pool.Query(ctx, `
		select id, user_id, raw_input, input_type, reason,
			coalesce(payload, 'null'::jsonb), resolved, created_at
		from review_queue
		where not resolved
		order by created_at, id
		limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list open review items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.ReviewItem, 0)
	for rows.Next() {
		var item domain.ReviewItem
		err = rows.Scan(
			&item.ID, &item.UserID, &item.RawInput, &item.InputType,
			&item.Reason, &item.Payload, &item.Resolved, &item.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan review item: %w", err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review items: %w", err)
	}
	return items, nil
}

func (r *ReviewRepo) Resolve(ctx context.Context, id int64) error {
	err := r.pool.QueryRow(ctx, `
		update review_queue set resolved = true where id = $1 returning id`, id).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("resolve review item: %w", err)
	}
	return nil
}
