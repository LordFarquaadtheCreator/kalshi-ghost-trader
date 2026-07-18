package kalshiclient

import (
	"context"
	"fmt"
	"net/url"
)

// Milestone maps the Kalshi milestone object. Milestones link event tickers
// to live-data feeds (scores, play-by-play) for sporting events.
type Milestone struct {
	ID                   string            `json:"id"`
	Category             string            `json:"category"`
	Type                 string            `json:"type"`
	StartDate            string            `json:"start_date"`
	EndDate              string            `json:"end_date"`
	RelatedEventTickers  []string          `json:"related_event_tickers"`
	PrimaryEventTickers  []string          `json:"primary_event_tickers"`
	Title                string            `json:"title"`
	NotificationMessage  string            `json:"notification_message"`
	SourceID             string            `json:"source_id"`
	SourceIDs            map[string]string `json:"source_ids"`
	Details              map[string]any    `json:"details"`
	LastUpdatedTS        string            `json:"last_updated_ts"`
}

// GetMilestonesResponse is the response from GET /milestones.
type GetMilestonesResponse struct {
	Milestones []Milestone `json:"milestones"`
	Cursor     string      `json:"cursor"`
}

// GetMilestones fetches milestones filtered by related event ticker.
// Returns all milestones for the given event.
func (c *Client) GetMilestones(ctx context.Context, relatedEventTicker string) ([]Milestone, error) {
	var all []Milestone
	cursor := ""
	for {
		params := url.Values{}
		params.Set("related_event_ticker", relatedEventTicker)
		params.Set("limit", "100")
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		var resp GetMilestonesResponse
		if err := c.get(ctx, "/milestones", params, &resp); err != nil {
			return nil, fmt.Errorf("get milestones (event=%s): %w", relatedEventTicker, err)
		}
		all = append(all, resp.Milestones...)
		if resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return all, nil
}

// LiveDataDetails holds the live score data for a tennis milestone.
// Kalshi returns a flexible object; we map the tennis-specific fields.
type LiveDataDetails struct {
	Competitor1ID               string         `json:"competitor1_id"`
	Competitor2ID               string         `json:"competitor2_id"`
	Competitor1OverallScore     int            `json:"competitor1_overall_score"`
	Competitor2OverallScore     int            `json:"competitor2_overall_score"`
	Competitor1CurrentRoundScore int           `json:"competitor1_current_round_score"`
	Competitor2CurrentRoundScore int           `json:"competitor2_current_round_score"`
	Competitor1RoundScores      []RoundScore   `json:"competitor1_round_scores"`
	Competitor2RoundScores      []RoundScore   `json:"competitor2_round_scores"`
	CompletedRounds             int            `json:"completed_rounds"`
	Server                      string         `json:"server"`
	Status                      string         `json:"status"`
	MatchStatus                 string         `json:"match_status"`
	Winner                      string         `json:"winner"`
	RoundWinners                []string       `json:"round_winners"`
}

// RoundScore is one set's score for a competitor.
type RoundScore struct {
	Outcome string `json:"outcome"` // "winner" or "loser"
	Score   int    `json:"score"`
}

// LiveData wraps the Kalshi live_data response.
type LiveData struct {
	Details     LiveDataDetails `json:"details"`
	MilestoneID string          `json:"milestone_id"`
	Type        string          `json:"type"`
}

// GetLiveDataResponse is the response from GET /live_data/milestone/{id}.
type GetLiveDataResponse struct {
	LiveData LiveData `json:"live_data"`
}

// GetLiveData fetches live score data for a specific milestone.
func (c *Client) GetLiveData(ctx context.Context, milestoneID string) (*LiveData, error) {
	var resp GetLiveDataResponse
	if err := c.get(ctx, "/live_data/milestone/"+milestoneID, nil, &resp); err != nil {
		return nil, fmt.Errorf("get live data (milestone=%s): %w", milestoneID, err)
	}
	return &resp.LiveData, nil
}
