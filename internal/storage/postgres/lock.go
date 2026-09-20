package postgres

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Locker hands out Postgres advisory locks, so a scheduled job that overruns
// its interval is skipped on the next tick instead of running twice.
type Locker struct {
	pool *pgxpool.Pool
}

func NewLocker(pool *pgxpool.Pool) *Locker {
	return &Locker{pool: pool}
}

// TryLock takes a session-scoped advisory lock keyed by name. The release
// function returns the connection to the pool, which drops the lock with it.
func (l *Locker) TryLock(ctx context.Context, key string) (bool, func(), error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("acquire connection for lock %s: %w", key, err)
	}

	var acquired bool
	err = conn.QueryRow(ctx, "select pg_try_advisory_lock($1)", lockKey(key)).Scan(&acquired)
	if err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("try advisory lock %s: %w", key, err)
	}

	if !acquired {
		conn.Release()
		return false, nil, nil
	}

	release := func() {
		_, unlockErr := conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", lockKey(key))
		if unlockErr != nil {
			log.Error().Err(unlockErr).Str("key", key).Msg("failed to release advisory lock")
		}
		conn.Release()
	}

	return true, release, nil
}

// lockKey folds a job name into the int64 advisory locks are keyed by.
func lockKey(name string) int64 {
	h := fnv.New64a()
	h.Write([]byte(name))
	return int64(h.Sum64())
}
