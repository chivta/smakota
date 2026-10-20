package api

import (
	"context"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const (
	// limiterIdleTTL is how long an IP may stay silent before its bucket is
	// dropped; it is far longer than a bucket takes to refill, so a swept
	// entry is indistinguishable from a fresh one.
	limiterIdleTTL = 5 * time.Minute
	// limiterSweepEvery is the sweep period.
	limiterSweepEvery = time.Minute

	retryAfterHeader = "Retry-After"
)

// RateLimit is the per-IP budget shared by the credential endpoints.
type RateLimit struct {
	PerMinute int
	Burst     int
}

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// ipLimiter keeps one token bucket per client IP. The map holds at most the
// IPs seen within limiterIdleTTL plus one sweep period.
type ipLimiter struct {
	mu      sync.Mutex
	entries map[string]*ipEntry
	limit   rate.Limit
	burst   int
}

func newIPLimiter(cfg RateLimit) *ipLimiter {
	return &ipLimiter{
		entries: make(map[string]*ipEntry),
		limit:   rate.Every(time.Minute / time.Duration(cfg.PerMinute)),
		burst:   cfg.Burst,
	}
}

// allow spends one token of ip's bucket. When refused it returns how long
// until a token is available.
func (l *ipLimiter) allow(ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[ip]
	if !ok {
		e = &ipEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.entries[ip] = e
	}
	e.lastSeen = now

	r := e.limiter.ReserveN(now, 1)
	delay := r.DelayFrom(now)
	if delay > 0 {
		// A refused request must not consume budget.
		r.CancelAt(now)
		return false, delay
	}
	return true, 0
}

func (l *ipLimiter) sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for ip, e := range l.entries {
		if now.Sub(e.lastSeen) > limiterIdleTTL {
			delete(l.entries, ip)
		}
	}
}

// run sweeps until ctx ends.
func (l *ipLimiter) run(ctx context.Context) {
	ticker := time.NewTicker(limiterSweepEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			l.sweep(now)
		}
	}
}

func (l *ipLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, delay := l.allow(c.ClientIP(), time.Now())
		if !ok {
			c.Header(retryAfterHeader, strconv.Itoa(int(math.Ceil(delay.Seconds()))))
			RespondWithError(c, domain.ErrRateLimited)
			return
		}
		c.Next()
	}
}
