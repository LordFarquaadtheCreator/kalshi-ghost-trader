// Package pricebands computes fixed-band price analysis of backtest strategy
// results, grouped by day. Results are persisted to the price_band_results
// table by a cron goroutine in main.go.
//
// Only days not already in the DB are computed — the cron diffs source days
// (derived from finalized market order timestamps) against computed days.
package pricebands

import (
	"fmt"
	"time"
)

// Band is a fixed price range bucket.
type Band struct {
	Lo, Hi float64
}

// FixedBands are the 12 standard price bands used for analysis.
var FixedBands = []Band{
	{0.01, 0.05},
	{0.05, 0.10},
	{0.10, 0.15},
	{0.15, 0.20},
	{0.20, 0.30},
	{0.30, 0.40},
	{0.40, 0.50},
	{0.50, 0.60},
	{0.60, 0.70},
	{0.70, 0.80},
	{0.80, 0.90},
	{0.90, 0.99},
}

// BandLabel returns the display label for a band.
func BandLabel(b Band) string {
	return fmt.Sprintf("%.2f-%.2f", b.Lo, b.Hi)
}

// FindBand returns the index of the band containing the price, or -1.
func FindBand(price float64) int {
	for i, b := range FixedBands {
		if price >= b.Lo && price < b.Hi {
			return i
		}
	}
	return -1
}

// Agg is a per-strategy-per-band aggregate.
type Agg struct {
	Strategy  string  `json:"strategy"`
	BandIdx   int     `json:"-"`
	BandLabel string  `json:"band_label"`
	BandLo    float64 `json:"band_lo"`
	BandHi    float64 `json:"band_hi"`
	N         int     `json:"n"`
	Wins      int     `json:"wins"`
	NetPnL    float64 `json:"net_pnl"`
	Invested  float64 `json:"invested"`
	EdgeSum   float64 `json:"-"`
}

// WinRate returns the win rate as a percentage.
func (a *Agg) WinRate() float64 {
	if a.N == 0 {
		return 0
	}
	return float64(a.Wins) / float64(a.N) * 100
}

// ROI returns return on investment as a percentage.
func (a *Agg) ROI() float64 {
	if a.Invested <= 0 {
		return 0
	}
	return a.NetPnL / a.Invested * 100
}

// AvgEdge returns the average edge in cents.
func (a *Agg) AvgEdge() float64 {
	if a.N == 0 {
		return 0
	}
	return a.EdgeSum / float64(a.N)
}

// TSToDay converts a unix timestamp (seconds or millis) to YYYY-MM-DD UTC.
func TSToDay(ts int64) string {
	var t time.Time
	if ts > 1e12 {
		t = time.UnixMilli(ts)
	} else {
		t = time.Unix(ts, 0)
	}
	return t.UTC().Format("2006-01-02")
}
