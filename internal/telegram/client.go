package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/arvlas/calbot/internal/domain"
)

// apiTimeout bounds every backend call. The PRD budgets 15 s from input to
// saved entry, so a hung request must fail well inside that.
const apiTimeout = 10 * time.Second

// Client talks to the backend HTTP API. The bot holds no business logic and no
// database handle: everything goes through these calls.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(baseURL, serviceToken string) *Client {
	return &Client{
		base:  baseURL,
		token: serviceToken,
		http:  &http.Client{Timeout: apiTimeout},
	}
}

// do issues one request on behalf of a Telegram user and decodes the response
// into out. A non-2xx status is translated back into an *AppError, so the
// caller sees the same vocabulary the API speaks.
func (c *Client) do(ctx context.Context, telegramID int64, method, path string, body, out any) error {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Telegram-User-ID", strconv.FormatInt(telegramID, 10))

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeAPIError(resp)
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}

	err = json.NewDecoder(resp.Body).Decode(out)
	if err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// decodeAPIError turns the API's machine-readable code back into the sentinel
// the bot's copy map is keyed by.
func decodeAPIError(resp *http.Response) error {
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)

	if body.Code == "" {
		return &domain.AppError{Status: resp.StatusCode, Code: domain.ErrInternal.Code}
	}
	return &domain.AppError{Status: resp.StatusCode, Code: body.Code}
}
