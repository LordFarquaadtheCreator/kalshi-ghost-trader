package backtest

import (
	"math"
	"sort"
)

// ScoreMetric defines how a set of orders is scored within a price band.
// The metric determines what "optimal" means and drives where bands split.
type ScoreMetric struct {
	Name  string `json:"name"`
	score func(orders []Order) float64
}

// WinRateScore rewards accuracy with sample-size weighting.
// Bands where conversion edge is consistently high score best.
var WinRateScore = ScoreMetric{
	Name: "winrate",
	score: func(orders []Order) float64 {
		if len(orders) == 0 {
			return 0
		}
		wins := 0
		for _, o := range orders {
			if o.Won {
				wins++
			}
		}
		wr := float64(wins) / float64(len(orders))
		return wr * math.Log(float64(len(orders)+1))
	},
}

// PnLScore rewards profit per signal with sample-size weighting.
// Uses mean P&L (not sum) because sum is additive — splitting never
// improves an additive score, so partitioning would never fire.
var PnLScore = ScoreMetric{
	Name: "pnl",
	score: func(orders []Order) float64 {
		if len(orders) == 0 {
			return 0
		}
		var pnl float64
		for _, o := range orders {
			pnl += o.PnL
		}
		mean := pnl / float64(len(orders))
		return mean * math.Log(float64(len(orders)+1))
	},
}

// ROIScore rewards capital efficiency with sample-size weighting.
// Bands where ROI is high AND there are enough signals score best.
var ROIScore = ScoreMetric{
	Name: "roi",
	score: func(orders []Order) float64 {
		if len(orders) == 0 {
			return 0
		}
		var invested, pnl float64
		for _, o := range orders {
			invested += o.Size * o.Price
			pnl += o.PnL
		}
		if invested <= 0 {
			return 0
		}
		roi := pnl / invested
		return roi * math.Log(float64(len(orders)+1))
	},
}

// SharpeScore rewards risk-adjusted return.
// Bands where mean P&L / std-dev is high score best.
var SharpeScore = ScoreMetric{
	Name: "sharpe",
	score: func(orders []Order) float64 {
		if len(orders) < 2 {
			return 0
		}
		var sum, sumSq float64
		for _, o := range orders {
			sum += o.PnL
			sumSq += o.PnL * o.PnL
		}
		n := float64(len(orders))
		mean := sum / n
		variance := sumSq/n - mean*mean
		if variance <= 0 {
			return 0
		}
		return (mean / math.Sqrt(variance)) * math.Sqrt(n)
	},
}

var scoreMetrics = map[string]ScoreMetric{
	"winrate": WinRateScore,
	"pnl":     PnLScore,
	"roi":     ROIScore,
	"sharpe":  SharpeScore,
}

// LookupScoreMetric returns the metric by name, defaulting to winrate.
func LookupScoreMetric(name string) ScoreMetric {
	if m, ok := scoreMetrics[name]; ok {
		return m
	}
	return WinRateScore
}

// PriceBand is a contiguous price range with computed performance stats.
// Boundaries are data-driven, not fixed buckets.
type PriceBand struct {
	MinPrice float64 `json:"min_price"`
	MaxPrice float64 `json:"max_price"`
	Signals  int     `json:"signals"`
	Wins     int     `json:"wins"`
	WinRate  float64 `json:"win_rate"`
	NetPnL   float64 `json:"net_pnl"`
	ROI      float64 `json:"roi"`
	AvgEdge  float64 `json:"avg_edge"`
	Score    float64 `json:"score"`
	IsPeak   bool    `json:"is_peak"`
}

// PriceBandResult holds bands and detected peaks for one strategy.
type PriceBandResult struct {
	Strategy string      `json:"strategy"`
	Metric   string      `json:"metric"`
	Bands    []PriceBand `json:"bands"`
	Peaks    []PriceBand `json:"peaks"`
}

// ComputePriceBands runs a strategy, then partitions its orders into
// adaptive price bands using recursive splitting driven by the score metric.
// Band width emerges from the data — sharp transitions get narrow bands,
// uniform regions stay wide.
func (e *Engine) ComputePriceBands(strategyName, metricName string, minSamples int) (*PriceBandResult, error) {
	if minSamples < 2 {
		minSamples = 2
	}
	metric := LookupScoreMetric(metricName)

	res, err := e.RunStrategy(strategyName, 0)
	if err != nil {
		return nil, err
	}

	orders := res.Orders
	if len(orders) < 2*minSamples {
		return &PriceBandResult{
			Strategy: strategyName,
			Metric:   metric.Name,
			Bands:    []PriceBand{makeBand(orders, metric, false)},
			Peaks:    nil,
		}, nil
	}

	sorted := make([]Order, len(orders))
	copy(sorted, orders)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Price < sorted[j].Price
	})

	bands := partition(sorted, metric, minSamples)
	detectPeaks(bands)

	var peaks []PriceBand
	for _, b := range bands {
		if b.IsPeak {
			peaks = append(peaks, b)
		}
	}
	sort.Slice(peaks, func(i, j int) bool {
		return peaks[i].Score > peaks[j].Score
	})

	return &PriceBandResult{
		Strategy: strategyName,
		Metric:   metric.Name,
		Bands:    bands,
		Peaks:    peaks,
	}, nil
}

// partition recursively splits orders into bands at the price boundary
// that maximizes score improvement. Stops when data doesn't support
// further splitting (improvement < 10% of current score).
func partition(orders []Order, metric ScoreMetric, minSamples int) []PriceBand {
	if len(orders) < 2*minSamples {
		return []PriceBand{makeBand(orders, metric, false)}
	}

	currentScore := metric.score(orders)

	bestIdx := -1
	bestImprovement := 0.0
	threshold := math.Abs(currentScore) * 0.10
	if threshold < 1e-9 {
		threshold = 1e-9
	}

	for i := minSamples; i <= len(orders)-minSamples; i++ {
		if orders[i].Price == orders[i-1].Price {
			continue
		}
		left := metric.score(orders[:i])
		right := metric.score(orders[i:])
		improvement := (left + right) - currentScore
		if improvement > bestImprovement {
			bestImprovement = improvement
			bestIdx = i
		}
	}

	if bestIdx < 0 || bestImprovement < threshold {
		return []PriceBand{makeBand(orders, metric, false)}
	}

	return append(
		partition(orders[:bestIdx], metric, minSamples),
		partition(orders[bestIdx:], metric, minSamples)...,
	)
}

// makeBand computes all stats for a set of orders within one band.
func makeBand(orders []Order, metric ScoreMetric, isPeak bool) PriceBand {
	b := PriceBand{
		Signals: len(orders),
		IsPeak:  isPeak,
	}
	if len(orders) == 0 {
		return b
	}

	b.MinPrice = orders[0].Price
	b.MaxPrice = orders[len(orders)-1].Price

	var invested, pnl, edgeSum float64
	for _, o := range orders {
		if o.Won {
			b.Wins++
		}
		pnl += o.PnL
		invested += o.Size * o.Price
		edgeSum += float64(o.EdgeCents)
	}
	b.NetPnL = math.Round(pnl*100) / 100
	b.WinRate = float64(b.Wins) / float64(b.Signals) * 100
	if invested > 0 {
		b.ROI = pnl / invested * 100
	}
	b.AvgEdge = edgeSum / float64(b.Signals)
	b.Score = metric.score(orders)

	return b
}

// detectPeaks marks bands that are local maxima above the median score.
func detectPeaks(bands []PriceBand) {
	if len(bands) < 3 {
		return
	}

	scores := make([]float64, len(bands))
	for i, b := range bands {
		scores[i] = b.Score
	}
	sort.Float64s(scores)
	median := scores[len(scores)/2]

	for i := range bands {
		if bands[i].Signals < 2 {
			continue
		}
		if bands[i].Score <= median {
			continue
		}
		left := i > 0
		right := i < len(bands)-1
		leftOK := !left || bands[i].Score > bands[i-1].Score
		rightOK := !right || bands[i].Score > bands[i+1].Score
		if leftOK && rightOK {
			bands[i].IsPeak = true
		}
	}
}
