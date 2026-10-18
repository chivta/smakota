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

// queryDate reads an optional ISO date query parameter.
func queryDate(c *gin.Context, name string, fallback time.Time) (time.Time, error) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, nil
	}

	day, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return time.Time{}, domain.ErrInvalidInput
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

// logSoftError records a failure that must not fail the request: the attempt
// log and the review queue are observability, not the user's business.
func logSoftError(err error, what string) {
	if err != nil {
		log.Error().Err(err).Msg(what)
	}
}
