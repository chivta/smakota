package api

import (
	"strconv"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/rs/zerolog/log"
)

// validate is built once at startup. Handlers never call validator.New().
var validate = validator.New()

// bindAndValidate decodes the body and checks it at the boundary. Everything
// below this line trusts its caller.
func bindAndValidate(c *gin.Context, target any) error {
	err := c.ShouldBindJSON(target)
	if err != nil {
		return domain.ErrInvalidInput
	}

	err = validate.Struct(target)
	if err != nil {
		return domain.ErrInvalidInput
	}
	return nil
}

func pathInt(c *gin.Context, name string) (int64, error) {
	v, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil {
		return 0, domain.ErrInvalidInput
	}
	return v, nil
}

// queryTime reads an optional time query parameter: a full RFC3339 timestamp,
// or a bare ISO date meaning that UTC day. For a bare date, end selects the
// day's last instant instead of its midnight.
func queryTime(c *gin.Context, name string, fallback time.Time, end bool) (time.Time, error) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, nil
	}

	t, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return t, nil
	}

	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, domain.ErrInvalidInput
	}
	if end {
		return endOfDay(day), nil
	}
	return day, nil
}

// queryInt reads an optional integer query parameter within [lo, hi].
func queryInt(c *gin.Context, name string, fallback, lo, hi int) (int, error) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, nil
	}

	v, err := strconv.Atoi(raw)
	if err != nil || v < lo || v > hi {
		return 0, domain.ErrInvalidInput
	}
	return v, nil
}

func truncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// endOfDay is the last instant Postgres can distinguish within the day, which
// makes a between-range on timestamptz inclusive of the whole day.
func endOfDay(day time.Time) time.Time {
	return truncateDay(day).AddDate(0, 0, 1).Add(-time.Microsecond)
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// applyIf copies a patch field onto the target when the client sent it.
func applyIf[T any](src *T, dst *T) {
	if src != nil {
		*dst = *src
	}
}

// logSoftError records a failure that must not fail the request: the review
// queue is observability, not the user's business.
func logSoftError(err error, what string) {
	if err != nil {
		log.Error().Err(err).Msg(what)
	}
}
