// Package flashscore implements a scraper for FlashScore's internal feed API,
// providing point-by-point tennis score data to complement Kalshi market prices.
//
// FlashScore has no public API. The scraper targets the internal feed at
// 2.flashscore.ninja/2/x/feed/ (region 2 = English locale). Authentication
// requires only the x-fsign: SW9D1eZo header — no TLS fingerprinting needed.
//
// Feed endpoints:
//   - f_2_<day>_1_en_1 — daily match feed (day: -1=today, 0=tomorrow, etc.)
//   - df_mh_1_<matchID> — point-by-point score data for a match
//   - dc_1_<matchID> — match metadata + current score
//
// The feed format is NOT JSON. It uses delimited text:
//   - '~' separates records (tournaments, matches, sets, games)
//   - '¬' separates fields within a record
//   - '÷' separates key from value
//
// The Scraper runs two concurrent loops: a feed scanner that fetches daily
// match feeds and maps FlashScore matches to Kalshi events via fuzzy player
// name matching, and a point poller that fetches point-by-point data for
// active matches and ingests new points via TickWriter.
//
// The scraper is disabled by default. Set flashscore_enabled: true in config.
package flashscore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client fetches data from FlashScore's internal feed API.
// Uses the geo-localized ninja subdomain (region 2 = English) since
// d.flashscore.com returns empty bodies from US IPs.
// Only requires x-fsign header — no TLS fingerprinting needed.
type Client struct {
	baseURL string
	http    *http.Client
	sign    string
}

const (
	defaultSign    = "SW9D1eZo"
	defaultBaseURL = "https://2.flashscore.ninja/2/x/feed"
)

// NewClient creates a FlashScore feed client.
// Transport sets ResponseHeaderTimeout so stalled connections fail fast
// instead of eating the full Client.Timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{
		baseURL: defaultBaseURL,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				ResponseHeaderTimeout: 5 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
		sign: defaultSign,
	}
}

// FetchDailyFeed returns the tennis daily feed for a given day offset.
// day: -1 = today, 0 = tomorrow, 1 = day after, etc.
// Sport 2 = tennis.
func (c *Client) FetchDailyFeed(ctx context.Context, day int) (string, error) {
	url := fmt.Sprintf("%s/f_2_%d_1_en_1", c.baseURL, day)
	return c.fetch(ctx, url)
}

// FetchPointByPoint returns the df_mh_1 endpoint data for a match.
// Contains point-by-point score data for all sets.
func (c *Client) FetchPointByPoint(ctx context.Context, matchID string) (string, error) {
	url := fmt.Sprintf("%s/df_mh_1_%s", c.baseURL, matchID)
	return c.fetch(ctx, url)
}

// FetchMatchInfo returns the dc_1 endpoint — match metadata + current score.
func (c *Client) FetchMatchInfo(ctx context.Context, matchID string) (string, error) {
	url := fmt.Sprintf("%s/dc_1_%s", c.baseURL, matchID)
	return c.fetch(ctx, url)
}

func (c *Client) fetch(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("x-fsign", c.sign)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "+
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	s := string(body)
	// FlashScore returns "0" for empty/invalid feeds
	if s == "0" || strings.TrimSpace(s) == "" {
		return "", nil
	}
	return s, nil
}

// parseInt is a safe integer parse that returns 0 on error.
func parseInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
