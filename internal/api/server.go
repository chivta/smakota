// Package api serves the HTTP interface. It holds no Telegram-specific logic:
// the bot and the Mini App are both clients of these routes.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/resolver"
	"github.com/arvlas/calbot/internal/storage/postgres"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The repository surfaces the handlers actually use, declared here on the
// consumer side. The concrete types live in internal/storage/postgres.
type userStore interface {
	GetByTelegramID(ctx context.Context, telegramID int64) (domain.User, error)
	Create(ctx context.Context, u domain.User) (domain.User, error)
	Update(ctx context.Context, u domain.User) error
}

type foodStore interface {
	GetByID(ctx context.Context, id int64) (domain.Food, error)
	GetByBarcode(ctx context.Context, barcode string) (domain.Food, error)
	Recents(ctx context.Context, userID int64, days, limit int) ([]postgres.RecentFood, error)
	CreatePersonal(ctx context.Context, userID int64, f domain.Food) (domain.Food, error)
	LastAmount(ctx context.Context, userID, foodID int64) (float64, bool, error)
}

type logStore interface {
	Add(ctx context.Context, e domain.LogEntry) (domain.LogEntry, error)
	Day(ctx context.Context, userID int64, day time.Time) ([]domain.LogEntry, error)
	UpdateGrams(ctx context.Context, userID, entryID int64, grams float64) error
	Delete(ctx context.Context, userID, entryID int64) error
	CopyDay(ctx context.Context, userID int64, from, to time.Time) (int, error)
}

type weightStore interface {
	Add(ctx context.Context, w domain.Weight) error
	RollingAverage(ctx context.Context, userID int64, days int) ([]postgres.WeightPoint, error)
}

type reviewStore interface {
	Add(ctx context.Context, item domain.ReviewItem) error
}

type attemptStore interface {
	Add(ctx context.Context, a domain.Attempt) error
}

type foodResolver interface {
	Resolve(ctx context.Context, req resolver.Request) (resolver.Result, error)
}

// Deps is the wiring surface. main builds it; nothing here reaches for a global.
type Deps struct {
	Users        userStore
	Foods        foodStore
	Logs         logStore
	Weights      weightStore
	Reviews      reviewStore
	Attempts     attemptStore
	Resolver     foodResolver
	BotToken     string
	ServiceToken string
	Production   bool
}

type Server struct {
	users        userStore
	foods        foodStore
	logs         logStore
	weights      weightStore
	reviews      reviewStore
	attempts     attemptStore
	resolver     foodResolver
	botToken     string
	serviceToken string
}

func New(d Deps) *Server {
	return &Server{
		users:        d.Users,
		foods:        d.Foods,
		logs:         d.Logs,
		weights:      d.Weights,
		reviews:      d.Reviews,
		attempts:     d.Attempts,
		resolver:     d.Resolver,
		botToken:     d.BotToken,
		serviceToken: d.ServiceToken,
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

	v1 := r.Group("/v1", s.auth())
	{
		v1.GET("/me", s.getMe)
		v1.PUT("/me", s.updateMe)

		v1.POST("/resolve", s.resolve)
		v1.GET("/foods/:id", s.getFood)
		v1.GET("/barcode/:code", s.getByBarcode)
		v1.POST("/foods/personal", s.createPersonalFood)
		v1.GET("/recents", s.getRecents)

		v1.GET("/days/:date", s.getDay)
		v1.POST("/entries", s.addEntry)
		v1.PATCH("/entries/:id", s.updateEntry)
		v1.DELETE("/entries/:id", s.deleteEntry)
		v1.POST("/days/:date/repeat", s.repeatDay)

		v1.POST("/weights", s.addWeight)
		v1.GET("/weights/trend", s.getWeightTrend)
	}

	// Onboarding creates the user, so it authenticates the caller without
	// requiring one to already exist.
	r.POST("/v1/register", s.register)

	return r
}
