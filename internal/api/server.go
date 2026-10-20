// Package api serves the HTTP interface. It holds no client-specific logic:
// every caller authenticates with a per-user bearer token.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/storage/postgres"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The repository surfaces the handlers actually use, declared here on the
// consumer side. The concrete types live in internal/storage/postgres.
type userStore interface {
	GetByID(ctx context.Context, id int64) (domain.User, error)
	Update(ctx context.Context, u domain.User) error
}

type tokenStore interface {
	Authenticate(ctx context.Context, hash []byte) (int64, error)
}

type foodStore interface {
	GetByID(ctx context.Context, id int64) (domain.Food, error)
	GetByBarcode(ctx context.Context, barcode string) (domain.Food, error)
	CreateContributed(ctx context.Context, userID int64, f domain.Food) (domain.Food, error)
	Search(ctx context.Context, p postgres.SearchParams) ([]postgres.Candidate, error)
	Frequent(ctx context.Context, userID int64, limit, offset int) ([]postgres.FrequentFood, error)
}

type logStore interface {
	Add(ctx context.Context, e domain.LogEntry) (domain.LogEntry, error)
	Range(ctx context.Context, userID int64, from, to time.Time, limit, offset int) ([]domain.LogEntry, error)
	UpdateGrams(ctx context.Context, userID, entryID int64, grams float64) error
	Delete(ctx context.Context, userID, entryID int64) error
}

type reviewStore interface {
	Add(ctx context.Context, item domain.ReviewItem) error
}

// Deps is the wiring surface. main builds it; nothing here reaches for a global.
type Deps struct {
	Users   userStore
	Tokens  tokenStore
	Foods   foodStore
	Logs    logStore
	Reviews reviewStore
}

type Server struct {
	users   userStore
	tokens  tokenStore
	foods   foodStore
	logs    logStore
	reviews reviewStore
}

func New(d Deps) *Server {
	return &Server{
		users:   d.Users,
		tokens:  d.Tokens,
		foods:   d.Foods,
		logs:    d.Logs,
		reviews: d.Reviews,
	}
}

// Router wires every route. /health and /metrics sit outside the auth group and
// outside the logging middleware.
func (s *Server) Router(production bool) *gin.Engine {
	if production {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery(), observability())

	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	s.registerDocs(r)
	if !production {
		s.registerDevUI(r)
	}

	v1 := r.Group("/v1", s.auth())
	{
		v1.GET("/me", s.getMe)
		v1.PUT("/me", s.updateMe)

		v1.GET("/foods", s.searchFoods)
		v1.GET("/foods/frequent", s.frequentFoods)
		v1.GET("/foods/:id", s.getFood)
		v1.GET("/barcode/:code", s.getByBarcode)
		v1.POST("/foods", s.createFood)

		v1.GET("/entries", s.listEntries)
		v1.POST("/entries", s.addEntry)
		v1.PATCH("/entries/:id", s.updateEntry)
		v1.DELETE("/entries/:id", s.deleteEntry)
	}

	return r
}
