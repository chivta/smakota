// Package mcp serves the food catalogue and the caller's diary to AI agents
// over MCP. Every tool is a thin call into the repositories the REST handlers
// use; the caller is whoever owns the bearer token, never a tool argument.
package mcp

import (
	"context"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/storage/postgres"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "calbot"
	serverVersion = "1.0.0"
)

type foodStore interface {
	GetByID(ctx context.Context, id int64) (domain.Food, error)
	CreateContributed(ctx context.Context, userID int64, f domain.Food) (domain.Food, error)
	Search(ctx context.Context, p postgres.SearchParams) ([]postgres.Candidate, error)
	Frequent(ctx context.Context, userID int64, limit, offset int) ([]postgres.FrequentFood, error)
}

type logStore interface {
	Add(ctx context.Context, e domain.LogEntry) (domain.LogEntry, error)
	Range(ctx context.Context, userID int64, from, to time.Time, limit, offset int) ([]domain.LogEntry, error)
}

type reviewStore interface {
	Add(ctx context.Context, item domain.ReviewItem) error
}

// Deps is the wiring surface; the api package fills it from its own Deps.
type Deps struct {
	Foods   foodStore
	Logs    logStore
	Reviews reviewStore
}

type userKey struct{}

// WithUser carries the authenticated user id into the tool handlers. It is the
// only way a tool learns who is calling.
func WithUser(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userKey{}, userID)
}

func currentUser(ctx context.Context) (int64, error) {
	id, ok := ctx.Value(userKey{}).(int64)
	if !ok {
		return 0, domain.ErrUnauthorized
	}
	return id, nil
}

type tools struct {
	foods   foodStore
	logs    logStore
	reviews reviewStore
}

// NewHandler returns the MCP endpoint. It is stateless: this API runs as one
// pod with no session affinity and has nothing to resume, so every request
// stands alone. Authentication is the caller's job and must run first.
func NewHandler(d Deps) http.Handler {
	t := &tools{foods: d.Foods, logs: d.Logs, reviews: d.Reviews}

	srv := sdk.NewServer(&sdk.Implementation{Name: serverName, Version: serverVersion}, nil)
	t.register(srv)

	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true},
	)
}
