// Package kalshiclient implements a REST API client for Kalshi market data endpoints.
//
// The Client signs all requests with RSA-PSS-SHA256 via [kalshiAuth.Signer],
// enforces a token-bucket rate limit, and retries on 429 responses with
// exponential backoff. Market data endpoints are public (no auth required),
// but all requests are signed for uniformity.
//
// Supported endpoints:
//   - GET /events — list events by series with cursor pagination
//   - GET /markets — list markets by series or event with cursor pagination
//   - GET /markets/{ticker} — fetch a single market by ticker
//
// Pagination is cursor-based: an empty cursor in the response signals the end.
// Maximum page sizes: 200 for events, 1000 for markets.
//
// Kalshi returns fixed-point values as strings (e.g. "0.65"); use [ParseFP]
// to convert. Timestamps are ISO-8601 strings with or without fractional
// seconds; use [ParseISOTime] to convert to unix milliseconds.
package kalshiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/config"
	"github.com/farquaad/kalshi-ghost-trader/internal/kalshiAuth"
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
	baseURL     string
	signer      *kalshiAuth.Signer
	http        *http.Client
	log         *slog.Logger
	rateLimiter *rateLimiter
}

// NewClient creates a REST client using config.Cfg for baseURL, timeout, and rate limit.
// signer may be nil for public endpoints.
func NewClient(signer *kalshiAuth.Signer, log *slog.Logger) *Client {
	return NewClientWithConfig(config.Cfg.RESTBaseURL, signer,
		time.Duration(config.Cfg.HTTPTimeoutSecs)*time.Second, config.Cfg.RateLimitRPS, log)
}

// NewClientWithConfig creates a REST client with explicit parameters.
// signer may be nil for public endpoints.
// httpTimeout of 0 uses defaultHTTPTimeout. rps sets the max requests/sec
// (0 uses defaultRateLimitRPS).
func NewClientWithConfig(baseURL string, signer *kalshiAuth.Signer, httpTimeout time.Duration, rps int, log *slog.Logger) *Client {
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
			return APIError{Code: resp.StatusCode, Body: string(body)}
		}

		return json.Unmarshal(body, out)
	}

	return fmt.Errorf("kalshi API: exhausted retries for %s", path)
}

// APIError is returned by get/post when the Kalshi API responds with a
// non-success status code. Callers can use errors.As to inspect the code
// and react accordingly (e.g. stop retrying on 404).
type APIError struct {
	Code int
	Body string
}

func (e APIError) Error() string {
	return fmt.Sprintf("kalshi API %d: %s", e.Code, e.Body)
}

// Post performs a signed POST request with a JSON body and decodes the response.
// Exported wrapper around internal post for use by order submission code.
func (c *Client) Post(ctx context.Context, path string, body any, out any) error {
	return c.post(ctx, path, body, out)
}

// post performs a signed POST request with a JSON body and decodes the response.
// Retries on 429 with exponential backoff (same as get).
// Logs per-attempt timing (connect/TLS/total) + status code for diagnosing
// hangs and slow responses.
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	fullURL := c.baseURL + path

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	backoff := initialRetryBackoff

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := c.rateLimiter.wait(ctx); err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		if c.signer != nil {
			signPath := apiPathPrefix + path
			headers, err := c.signer.AuthHeaders("POST", signPath)
			if err != nil {
				return fmt.Errorf("sign request: %w", err)
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}

		start := time.Now()
		resp, err := c.http.Do(req)
		total := time.Since(start)
		if err != nil {
			c.log.Warn("kalshi post: request failed",
				"path", path, "attempt", attempt+1,
				"total_ms", total.Milliseconds(), "error", err)
			return err
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			c.log.Warn("kalshi post: read body failed",
				"path", path, "attempt", attempt+1,
				"status", resp.StatusCode, "total_ms", total.Milliseconds(), "error", err)
			return err
		}

		c.log.Info("kalshi post: response",
			"path", path, "attempt", attempt+1,
			"status", resp.StatusCode, "total_ms", total.Milliseconds(),
			"body_bytes", len(respBody))

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

		if resp.StatusCode != 200 && resp.StatusCode != 201 {
			return APIError{Code: resp.StatusCode, Body: string(respBody)}
		}

		return json.Unmarshal(respBody, out)
	}

	return fmt.Errorf("kalshi API: exhausted retries for %s", path)
}
