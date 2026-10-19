package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/arvlas/calbot/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const userIDKey = "user_id"

// observability logs and times every request. It skips /health and /metrics, so
// the probes do not drown the log.
func observability() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health" || path == "/metrics" || strings.HasPrefix(path, "/docs") || path == "/dev" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		elapsed := time.Since(start)

		status := strconv.Itoa(c.Writer.Status())
		metrics.HTTPRequests.WithLabelValues(c.Request.Method, c.FullPath(), status).
			Observe(elapsed.Seconds())

		event := log.Info()
		if c.Writer.Status() >= 500 {
			event = log.Error()
		}
		event.
			Str("method", c.Request.Method).
			Str("path", path).
			Int("status", c.Writer.Status()).
			Dur("elapsed", elapsed).
			Msg("request")
	}
}

// currentUser pulls the authenticated user ID that auth put on the context.
func currentUser(c *gin.Context) (int64, error) {
	v, ok := c.Get(userIDKey)
	if !ok {
		return 0, domain.ErrUnauthorized
	}
	id, ok := v.(int64)
	if !ok {
		return 0, domain.ErrUnauthorized
	}
	return id, nil
}
