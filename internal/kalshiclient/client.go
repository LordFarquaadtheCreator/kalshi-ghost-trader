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
)

// Client is the Kalshi REST API client. Market data endpoints are public
// (no auth needed), but we sign all requests anyway for uniformity.
type Client struct {
	baseURL string
	signer  *kalshiauth.Signer
	http    *http.Client
	log     *slog.Logger
}

// NewClient creates a REST client. signer may be nil for public endpoints.
// httpTimeout of 0 uses defaultHTTPTimeout.
func NewClient(baseURL string, signer *kalshiauth.Signer, httpTimeout time.Duration, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	if httpTimeout <= 0 {
		httpTimeout = defaultHTTPTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		signer:  signer,
		http: &http.Client{
			Timeout: httpTimeout,
		},
		log: log,
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
