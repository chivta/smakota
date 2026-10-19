// Command calbot is the single binary behind every process in the deployment.
// The subcommand picks the role: api, scheduler, ingest <name>, seed, reindex-aliases, or the user and token commands.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/arvlas/calbot/internal/config"
	"github.com/arvlas/calbot/internal/logging"
	"github.com/arvlas/calbot/internal/metrics"
	"github.com/rs/zerolog/log"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	logging.Init(cfg.LogLevel, cfg.IsProduction())
	metrics.Init()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err = run(ctx, cfg, os.Args[1:])
	if err != nil {
		log.Error().Err(err).Msg("exiting")
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "api":
		return runAPI(ctx, cfg)
	case "scheduler":
		return runScheduler(ctx, cfg)
	case "ingest":
		if len(args) < 2 {
			return fmt.Errorf("ingest needs a source name")
		}
		return runIngest(ctx, cfg, args[1])
	case "seed":
		return runSeed(ctx, cfg)
	case "reindex-aliases":
		return runReindexAliases(ctx, cfg, args[1:])
	case "migrate":
		return runMigrate(ctx, cfg)
	case "health":
		return runHealth(ctx, cfg)
	case "user":
		return runUser(ctx, cfg, args[1:])
	case "token":
		return runToken(ctx, cfg, args[1:])
	}

	return usage()
}

func usage() error {
	return fmt.Errorf("usage: calbot <api|scheduler|ingest NAME|seed|reindex-aliases [AFTER_ID]|migrate|health|user create|token issue USER_ID [NAME]|token revoke TOKEN_ID>")
}
