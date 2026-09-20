package ingest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const maxHTTPAttempts = 5

type cachedResponse struct {
	etag       string
	statusCode int
	header     http.Header
	body       []byte
}

// HTTPClient provides the shared transport policy used by every adapter.
// Its ETag cache lives only as long as this value, normally one ingest run.
type HTTPClient struct {
	client    *http.Client
	userAgent string

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	cache    map[string]cachedResponse
}

// NewHTTPClient constructs the shared ingest client.
func NewHTTPClient(client *http.Client, userAgent string) *HTTPClient {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &HTTPClient{
		client:    client,
		userAgent: userAgent,
		limiters:  make(map[string]*rate.Limiter),
		cache:     make(map[string]cachedResponse),
	}
}

// ResetCache starts a new run-scoped ETag cache.
func (c *HTTPClient) ResetCache() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]cachedResponse)
}

// Get performs a rate-limited GET. cacheETag must be false for streaming
// downloads because caching requires holding the response body in memory.
func (c *HTTPClient) Get(ctx context.Context, source, rawURL string, requestsPerSecond rate.Limit, cacheETag bool) (*http.Response, error) {
	limiter := c.limiter(source, requestsPerSecond)
	var lastStatus int
	for attempt := 0; attempt < maxHTTPAttempts; attempt++ {
		err := limiter.Wait(ctx)
		if err != nil {
			return nil, fmt.Errorf("wait for %s rate limit: %w", source, err)
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create %s request: %w", source, err)
		}
		request.Header.Set("User-Agent", c.userAgent)
		if cacheETag {
			cached, ok := c.cached(rawURL)
			if ok && cached.etag != "" {
				request.Header.Set("If-None-Match", cached.etag)
			}
		}

		response, err := c.client.Do(request)
		if err != nil {
			if attempt == maxHTTPAttempts-1 {
				return nil, fmt.Errorf("send %s request: %w", source, err)
			}
			if err = waitBackoff(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}

		lastStatus = response.StatusCode
		if response.StatusCode == http.StatusNotModified && cacheETag {
			response.Body.Close()
			cached, ok := c.cached(rawURL)
			if !ok {
				return nil, fmt.Errorf("%s returned 304 without a cached response", source)
			}
			return responseFromCache(request, cached), nil
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			delay := retryDelay(response, attempt)
			response.Body.Close()
			if attempt == maxHTTPAttempts-1 {
				break
			}
			if err = waitBackoff(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return nil, fmt.Errorf("%s request returned HTTP %d", source, response.StatusCode)
		}
		if cacheETag && response.Header.Get("ETag") != "" {
			response, err = c.remember(request, response)
			if err != nil {
				return nil, fmt.Errorf("cache %s response: %w", source, err)
			}
		}
		return response, nil
	}
	return nil, fmt.Errorf("%s request failed after %d attempts with HTTP %d", source, maxHTTPAttempts, lastStatus)
}

func (c *HTTPClient) limiter(source string, requestsPerSecond rate.Limit) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	limiter, ok := c.limiters[source]
	if !ok {
		limiter = rate.NewLimiter(requestsPerSecond, 1)
		c.limiters[source] = limiter
	}
	return limiter
}

func (c *HTTPClient) cached(rawURL string) (cachedResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cached, ok := c.cache[rawURL]
	return cached, ok
}

func (c *HTTPClient) remember(request *http.Request, response *http.Response) (*http.Response, error) {
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	cached := cachedResponse{
		etag:       response.Header.Get("ETag"),
		statusCode: response.StatusCode,
		header:     response.Header.Clone(),
		body:       body,
	}
	c.mu.Lock()
	c.cache[request.URL.String()] = cached
	c.mu.Unlock()
	return responseFromCache(request, cached), nil
}

func responseFromCache(request *http.Request, cached cachedResponse) *http.Response {
	return &http.Response{
		StatusCode:    cached.statusCode,
		Status:        fmt.Sprintf("%d %s", cached.statusCode, http.StatusText(cached.statusCode)),
		Header:        cached.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(cached.body)),
		ContentLength: int64(len(cached.body)),
		Request:       request,
	}
}

func retryDelay(response *http.Response, attempt int) time.Duration {
	retryAfter := response.Header.Get("Retry-After")
	seconds, err := strconv.Atoi(retryAfter)
	if err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(retryAfter)
	if err == nil {
		delay := time.Until(when)
		if delay > 0 {
			return delay
		}
	}
	return backoff(attempt)
}

func backoff(attempt int) time.Duration {
	return time.Second << attempt
}

func waitBackoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
