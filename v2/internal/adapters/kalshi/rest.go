package kalshi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/adapters/kalshi/wire"
)

// RESTClient is a minimal Kalshi REST client for events/markets scanning.
// Signs requests via the Signer interface (same as Exchange).
type RESTClient struct {
	baseURL string
	signer  Signer
	http    *http.Client
}

// NewRESTClient creates a REST client. signer may be nil for public endpoints.
func NewRESTClient(baseURL string, signer Signer, timeoutSecs int) *RESTClient {
	if timeoutSecs <= 0 {
		timeoutSecs = 30
	}
	return &RESTClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		signer:  signer,
		http:    &http.Client{Timeout: time.Duration(timeoutSecs) * time.Second},
	}
}

// GetEvents fetches all events for a series with cursor pagination.
func (c *RESTClient) GetEvents(ctx context.Context, seriesTicker string) ([]wire.EventData, error) {
	var all []wire.EventData
	cursor := ""
	for {
		params := url.Values{}
		params.Set("series_ticker", seriesTicker)
		params.Set("limit", "200")
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var resp wire.GetEventsResponse
		if err := c.get(ctx, "/events", params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Events...)
		if resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return all, nil
}

// GetMarkets fetches all markets for an event with cursor pagination.
func (c *RESTClient) GetMarkets(ctx context.Context, eventTicker string) ([]wire.MarketData, error) {
	var all []wire.MarketData
	cursor := ""
	for {
		params := url.Values{}
		params.Set("event_ticker", eventTicker)
		params.Set("limit", "1000")
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var resp wire.GetMarketsResponse
		if err := c.get(ctx, "/markets", params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Markets...)
		if resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return all, nil
}

func (c *RESTClient) get(ctx context.Context, path string, params url.Values, out any) error {
	fullURL := c.baseURL + path
	if len(params) > 0 {
		fullURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.signer != nil {
		signPath := "/trade-api/v2" + path
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
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("kalshi API %d: %s", resp.StatusCode, string(body))
	}

	return json.Unmarshal(body, out)
}

// post is shared with Exchange but kept here for completeness.
func (c *RESTClient) post(ctx context.Context, path string, body any, out any) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	fullURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	if c.signer != nil {
		signPath := "/trade-api/v2" + path
		headers, err := c.signer.AuthHeaders("POST", signPath)
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
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("kalshi API %d: %s", resp.StatusCode, string(respBody))
	}

	return json.Unmarshal(respBody, out)
}

// ParseISOTime converts Kalshi ISO-8601 timestamp to unix millis.
func ParseISOTime(s string) int64 {
	if s == "" {
		return 0
	}
	// Try with fractional seconds
	if strings.Contains(s, ".") {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err == nil {
			return t.UnixMilli()
		}
	}
	// Try without fractional seconds
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t.UnixMilli()
	}
	return 0
}

// ParseFP converts a Kalshi fixed-point string to float64.
func ParseFP(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
