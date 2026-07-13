package kalshiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiauth"
)

const (
	// apiPathPrefix is prepended to relative paths for request signing.
	// Kalshi requires the full path from API root (e.g. "/trade-api/v2/events").
	apiPathPrefix = "/trade-api/v2"

	// httpTimeout caps each REST request. Overridable via config.
	defaultHTTPTimeout = 30 * time.Second

	// maxRetries for 429 rate-limit backoff.
	maxRetries = 3

	// initialRetryBackoff is the starting backoff for 429 retries. Doubles each attempt.
	initialRetryBackoff = time.Second

	// defaultRateLimitRPS is the default max requests/sec.
	// Basic tier: 200 tokens/sec ÷ 10 tokens/request = 20 req/sec.
	// Conservative default leaves headroom.
	defaultRateLimitRPS = 15
)

// Client is the Kalshi REST API client. Market data endpoints are public
// (no auth needed), but we sign all requests anyway for uniformity.
// Includes a token-bucket rate limiter to avoid 429s.
type Client struct {
	baseURL   string
	signer    *kalshiauth.Signer
	http      *http.Client
	log       *slog.Logger
	rateLimiter *rateLimiter
}

// NewClient creates a REST client. signer may be nil for public endpoints.
// httpTimeout of 0 uses defaultHTTPTimeout. rps sets the max requests/sec
// (0 uses defaultRateLimitRPS).
func NewClient(baseURL string, signer *kalshiauth.Signer, httpTimeout time.Duration, rps int, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	if httpTimeout <= 0 {
		httpTimeout = defaultHTTPTimeout
	}
	if rps <= 0 {
		rps = defaultRateLimitRPS
	}
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		signer:      signer,
		http:        &http.Client{Timeout: httpTimeout},
		log:         log,
		rateLimiter: newRateLimiter(rps),
	}
}

// get performs a signed GET request and decodes JSON.
// Retries on 429 with exponential backoff (per Kalshi rate limit docs).
func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	fullURL := c.baseURL + path
	if params != nil && len(params) > 0 {
		fullURL += "?" + params.Encode()
	}

	backoff := initialRetryBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Throttle before each request attempt (including retries)
		if err := c.rateLimiter.wait(ctx); err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		// Sign the request — Kalshi requires full path from API root
		if c.signer != nil {
			signPath := apiPathPrefix + path
			headers, err := c.signer.AuthHeaders("GET", signPath)
			if err != nil {
				return fmt.Errorf("sign request: %w", err)
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}

		if resp.StatusCode == 429 && attempt < maxRetries {
			c.log.Warn("rate limited, retrying", "path", path, "attempt", attempt+1, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode != 200 {
			return fmt.Errorf("kalshi API %d: %s", resp.StatusCode, string(body))
		}

		return json.Unmarshal(body, out)
	}

	return fmt.Errorf("kalshi API: exhausted retries for %s", path)
}
