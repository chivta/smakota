package postgres

import (
	"context"
	"fmt"

	"github.com/arvlas/calbot/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AttemptRepo struct {
	pool *pgxpool.Pool
}

func NewAttemptRepo(pool *pgxpool.Pool) *AttemptRepo {
	return &AttemptRepo{pool: pool}
}

func (r *AttemptRepo) Add(ctx context.Context, a domain.Attempt) error {
	_, err := r.pool.Exec(ctx, `
		insert into input_attempts (
			user_id, input_type, latency_ms, first_candidate_accepted,
			refinement_used, resolved, food_id, created_at
		) values (nullif($1, 0), $2, $3, $4, $5, $6, $7, coalesce($8, now()))`,
		a.UserID, a.InputType, a.LatencyMS, a.FirstCandidateAccepted,
		a.RefinementUsed, a.Resolved, a.FoodID, nullableTime(a.CreatedAt))
	if err != nil {
		return fmt.Errorf("add input attempt: %w", err)
	}
	return nil
}
