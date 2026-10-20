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

// lastUsedGranularity bounds how often a token's last_used_at is rewritten.
const lastUsedGranularity = time.Minute

type TokenRepo struct {
	pool *pgxpool.Pool
}

func NewTokenRepo(pool *pgxpool.Pool) *TokenRepo {
	return &TokenRepo{pool: pool}
}

// Issue stores the hash of a new token and returns the token's id.
func (r *TokenRepo) Issue(ctx context.Context, userID int64, name string, hash []byte) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		insert into api_tokens (user_id, name, token_hash)
		values ($1, $2, $3)
		returning id`,
		userID, name, hash,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("issue token: %w", err)
	}
	return id, nil
}

// Revoke is idempotent: revoking a revoked token keeps its first revoked_at.
func (r *TokenRepo) Revoke(ctx context.Context, tokenID int64) error {
	var id int64
	err := r.pool.QueryRow(ctx, `
		update api_tokens set revoked_at = coalesce(revoked_at, now())
		where id = $1
		returning id`,
		tokenID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

// Authenticate returns the owner and the id of a live token and stamps
// last_used_at in the same round trip, skipping the write when the stamp is
// already recent.
func (r *TokenRepo) Authenticate(ctx context.Context, hash []byte) (userID, tokenID int64, err error) {
	err = r.pool.QueryRow(ctx, `
		with live as (
			select id, user_id from api_tokens
			where token_hash = $1 and revoked_at is null
		), touched as (
			update api_tokens set last_used_at = now()
			where id in (select id from live)
				and (last_used_at is null or last_used_at < now() - make_interval(secs => $2))
		)
		select user_id, id from live`,
		hash, lastUsedGranularity.Seconds(),
	).Scan(&userID, &tokenID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, domain.ErrUnauthorized
	}
	if err != nil {
		return 0, 0, fmt.Errorf("authenticate token: %w", err)
	}
	return userID, tokenID, nil
}
