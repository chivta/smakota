package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/config"
)

// healthTimeout keeps a container healthcheck well inside its own interval.
const healthTimeout = 3 * time.Second

// runHealth probes the local API and exits non-zero if it is not serving. The
// production image is distroless and has no shell or wget, so the healthcheck
// has to be the binary itself.
func runHealth(ctx context.Context, cfg config.Config) error {
	host, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("parse listen address %s: %w", cfg.HTTPAddr, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}

	probeCtx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	url := fmt.Sprintf("http://%s/health", net.JoinHostPort(host, port))
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}
