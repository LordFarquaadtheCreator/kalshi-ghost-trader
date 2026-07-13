package kalshiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const (
	// marketsPerPage is the max page size for GET /markets (Kalshi API limit).
	marketsPerPage = "1000"
)

// GetMarketsResponse is the response from GET /markets.
type GetMarketsResponse struct {
	Markets []MarketData `json:"markets"`
	Cursor  string       `json:"cursor"`
}

// MarketData maps the Kalshi market object. All fixed-point fields come as strings.
type MarketData struct {
	Ticker                 string          `json:"ticker"`
	EventTicker            string          `json:"event_ticker"`
	MarketType             string          `json:"market_type"`
	Title                  string          `json:"title"`
	YesSubTitle            string          `json:"yes_sub_title"`
	NoSubTitle             string          `json:"no_sub_title"`
	CreatedTime            string          `json:"created_time"`
	UpdatedTime            string          `json:"updated_time"`
	OpenTime               string          `json:"open_time"`
	CloseTime              string          `json:"close_time"`
	ExpectedExpirationTime string          `json:"expected_expiration_time"`
	ExpirationTime         string          `json:"expiration_time"`
	LatestExpirationTime   string          `json:"latest_expiration_time"`
	OccurrenceDatetime     string          `json:"occurrence_datetime"`
	SettlementTimerSeconds int             `json:"settlement_timer_seconds"`
	Status                 string          `json:"status"`
	NotionalValueDollars   string          `json:"notional_value_dollars"`
	YesBidDollars          string          `json:"yes_bid_dollars"`
	YesAskDollars          string          `json:"yes_ask_dollars"`
	YesBidSizeFP           string          `json:"yes_bid_size_fp"`
	YesAskSizeFP           string          `json:"yes_ask_size_fp"`
	NoBidDollars           string          `json:"no_bid_dollars"`
	NoAskDollars           string          `json:"no_ask_dollars"`
	LastPriceDollars       string          `json:"last_price_dollars"`
	PreviousYesBidDollars  string          `json:"previous_yes_bid_dollars"`
	PreviousYesAskDollars  string          `json:"previous_yes_ask_dollars"`
	PreviousPriceDollars   string          `json:"previous_price_dollars"`
	VolumeFP               string          `json:"volume_fp"`
	Volume24hFP            string          `json:"volume_24h_fp"`
	OpenInterestFP         string          `json:"open_interest_fp"`
	Result                 string          `json:"result"`
	CanCloseEarly          bool            `json:"can_close_early"`
	ExpirationValue        string          `json:"expiration_value"`
	SettlementValueDollars string          `json:"settlement_value_dollars"`
	SettlementTS           string          `json:"settlement_ts"`
	RulesPrimary           string          `json:"rules_primary"`
	RulesSecondary         string          `json:"rules_secondary"`
	PriceLevelStructure    string          `json:"price_level_structure"`
	StrikeType             string          `json:"strike_type"`
	CustomStrike           json.RawMessage `json:"custom_strike"`
	EarlyCloseCondition    string          `json:"early_close_condition"`
	LiquidityDollars       string          `json:"liquidity_dollars"`
}

// CustomStrikeTennis holds the tennis competitor UUID.
type CustomStrikeTennis struct {
	TennisCompetitor string `json:"tennis_competitor"`
}

// GetMarkets fetches markets for a series or event with optional status filter.
// Paginates through all results.
func (c *Client) GetMarkets(ctx context.Context, seriesTicker, eventTicker, status string) ([]MarketData, error) {
	var all []MarketData
	cursor := ""
	for {
		params := url.Values{}
		if seriesTicker != "" {
			params.Set("series_ticker", seriesTicker)
		}
		if eventTicker != "" {
			params.Set("event_ticker", eventTicker)
		}
		if status != "" {
			params.Set("status", status)
		}
		params.Set("limit", marketsPerPage)
		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var resp GetMarketsResponse
		if err := c.get(ctx, "/markets", params, &resp); err != nil {
			return nil, fmt.Errorf("get markets: %w", err)
		}
		all = append(all, resp.Markets...)
		if resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return all, nil
}

// GetMarket fetches a single market by ticker.
func (c *Client) GetMarket(ctx context.Context, ticker string) (*MarketData, error) {
	var resp struct {
		Market MarketData `json:"market"`
	}
	if err := c.get(ctx, "/markets/"+ticker, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Market, nil
}
