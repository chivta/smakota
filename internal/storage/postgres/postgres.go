// Package postgres owns every database access path. Repository types are
// per-entity, each holding the shared pool.
package postgres

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"
	"github.com/rs/zerolog/log"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Connect opens the pool and verifies it. Migrations are a separate call so the
// caller decides when they run.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	err = pool.Ping(ctx)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}

// Migrate runs every pending migration. It runs on API startup, so deployment
// has no separate migration step.
func Migrate(ctx context.Context, url string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("connect for migrations: %w", err)
	}
	defer conn.Close(ctx)

	migrator, err := migrate.NewMigrator(ctx, conn, "schema_version")
	if err != nil {
		return fmt.Errorf("new migrator: %w", err)
	}

	sub, err := fs_sub()
	if err != nil {
		return err
	}

	err = migrator.LoadMigrations(sub)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	before, err := migrator.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("current version: %w", err)
	}

	err = migrator.Migrate(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	after, err := migrator.GetCurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("current version: %w", err)
	}

	log.Info().Int32("from", before).Int32("to", after).Msg("migrations applied")
	return nil
}
