package kalshiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const (
	// eventsPerPage is the max page size for GET /events (Kalshi API limit).
	eventsPerPage = "200"
)

// GetEventsResponse is the response from GET /events.
type GetEventsResponse struct {
	Events     []EventData      `json:"events"`
	Milestones []json.RawMessage `json:"milestones"`
	Cursor     string           `json:"cursor"`
}

// EventData maps the Kalshi event object.
type EventData struct {
	EventTicker          string             `json:"event_ticker"`
	SeriesTicker         string             `json:"series_ticker"`
	Title                string             `json:"title"`
	SubTitle             string             `json:"sub_title"`
	Category             string             `json:"category"`
	CollateralReturnType string             `json:"collateral_return_type"`
	MutuallyExclusive    bool               `json:"mutually_exclusive"`
	AvailableOnBrokers   bool               `json:"available_on_brokers"`
	SettlementSources    []SettlementSource `json:"settlement_sources"`
	StrikePeriod         string             `json:"strike_period"`
	ProductMetadata      ProductMetadata    `json:"product_metadata"`
	LastUpdatedTS        string             `json:"last_updated_ts"`
}

type SettlementSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ProductMetadata for tennis: { "competition": "ATP Bastad", "competition_scope": "Game" }
type ProductMetadata struct {
	Competition      string `json:"competition"`
	CompetitionScope string `json:"competition_scope"`
}

// GetEvents fetches events for a series with optional status filter.
// Paginates through all results.
func (c *Client) GetEvents(ctx context.Context, seriesTicker, status string) ([]EventData, error) {
	var all []EventData
	cursor := ""
	for {
		params := url.Values{}
		params.Set("series_ticker", seriesTicker)
		if status != "" {
			params.Set("status", status)
		}
		params.Set("limit", eventsPerPage)
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var resp GetEventsResponse
		if err := c.get(ctx, "/events", params, &resp); err != nil {
			return nil, fmt.Errorf("get events (series=%s): %w", seriesTicker, err)
		}
		all = append(all, resp.Events...)
		if resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return all, nil
}
