package strategy

import (
	"math"
	"math/rand"
	"strconv"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/features"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/dmitryikh/leaves"
)

// Mode controls how a LearnedStrategy behaves.
type Mode int

const (
	ModeShadow   Mode = iota // compute intents, don't execute
	ModePaper                // execute paper orders with stochastic exploration
	ModeChampion             // eligible for real trading (human-gated separately)
)

// Predictor is the interface for model inference (LightGBM or MLP).
type Predictor interface {
	Predict(values []float64) (float64, error)
	Name() string
}

// LearnedStrategy is a strategy whose thresholds come from a trained artifact.
// It satisfies the existing Strategy interface — everything downstream keys
// on strategy name and needs no changes.
type LearnedStrategy struct {
	name     string
	modelID  int64
	feat     *features.DefaultExtractor
	featNames []string
	model    Predictor
	mode     Mode
	rng      *rand.Rand
	temp     float64

	// Preallocated feature buffer — reused to avoid hot-path allocation.
	featBuf []float64
}

// NewLearnedStrategy creates a learned strategy. rng is used only in Paper mode.
func NewLearnedStrategy(name string, modelID int64, model Predictor, mode Mode, rng *rand.Rand, temp float64) *LearnedStrategy {
	feat := features.NewDefaultExtractor()
	return &LearnedStrategy{
		name:      name,
		modelID:   modelID,
		feat:      feat,
		featNames: feat.Names(),
		model:     model,
		mode:      mode,
		rng:       rng,
		temp:      temp,
		featBuf:   make([]float64, len(feat.Names())),
	}
}

// Name returns the strategy name (e.g. "rl.fairvalue.v37").
func (s *LearnedStrategy) Name() string { return s.name }

// ModelID returns the model registry ID.
func (s *LearnedStrategy) ModelID() int64 { return s.modelID }

// Mode returns the current mode.
func (s *LearnedStrategy) Mode() Mode { return s.mode }

// SetMode updates the mode. Called by the registry poller at match boundaries.
func (s *LearnedStrategy) SetMode(m Mode) { s.mode = m }

// FeatureHash returns the feature hash for this strategy's extractor.
func (s *LearnedStrategy) FeatureHash() string { return s.feat.Hash() }

// OnEvent implements the Strategy interface. Pure, no I/O.
func (s *LearnedStrategy) OnEvent(ev match.Event, st *State) []match.Intent {
	// Build the feature view from the match state.
	v := viewFromMatchView(st.MatchView, "")

	// Extract features into the preallocated buffer (zero-alloc hot path).
	vec := s.feat.ExtractInto(s.featBuf, v, ev)
	s.featBuf = vec // retain for next call

	// Run inference.
	pred, err := s.model.Predict(s.featBuf)
	if err != nil {
		return nil
	}

	// Convert prediction to fair value in cents.
	fairValueCents := int(math.Round(pred * 100))
	if fairValueCents < 1 {
		fairValueCents = 1
	}
	if fairValueCents > 99 {
		fairValueCents = 99
	}

	// Compute edge.
	price := v.PriceCents
	if price == 0 {
		return nil
	}
	edge := fairValueCents - price
	if edge <= 0 {
		return nil
	}

	// Decide action based on mode.
	var action string
	var propensity *float64

	if s.mode == ModePaper && s.temp > 0 && s.rng != nil {
		// Stochastic exploration: sample from softmax over {pass, buy}.
		// Value of buying = edge, value of passing = 0.
		edgeFloat := float64(edge)
		buyLogit := edgeFloat / s.temp
		passLogit := 0.0
		maxLogit := math.Max(buyLogit, passLogit)
		buyProb := math.Exp(buyLogit - maxLogit)
		passProb := math.Exp(passLogit - maxLogit)
		total := buyProb + passProb
		buyProb /= total

		if s.rng.Float64() < buyProb {
			action = "buy"
			p := buyProb
			propensity = &p
		} else {
			return nil // pass
		}
	} else {
		// Deterministic: argmax — buy if edge > 0.
		action = "buy"
		p := 1.0
		propensity = &p
	}

	// Build the feature map for logging.
	featMap := make(map[string]float64, len(s.featNames))
	for i, name := range s.featNames {
		featMap[name] = vec[i]
	}

	intent := match.Intent{
		MarketTicker: evMarketTicker(ev),
		Strategy:     s.name,
		Action:       action,
		PriceCents:   price,
		ConvProbBps:  fairValueCents * 100,
		Reason:       "learned: edge=" + itoa(edge),
		FeatureHash:  s.feat.Hash(),
		Features:     featMap,
		ModelID:      &s.modelID,
		Propensity:   propensity,
	}

	return []match.Intent{intent}
}

// evMarketTicker extracts the market ticker from an event.
func evMarketTicker(ev match.Event) string {
	switch e := ev.(type) {
	case match.PriceUpdate:
		return e.MarketTicker
	case match.LifecycleChange:
		return e.MarketTicker
	default:
		return ""
	}
}

// itoa is a minimal int→string to avoid strconv on the hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// LightGBMPredictor wraps leaves.Ensemble as a Predictor.
type LightGBMPredictor struct {
	ensemble *leaves.Ensemble
	name     string
}

// NewLightGBMPredictor loads a LightGBM model from a file.
func NewLightGBMPredictor(path string) (*LightGBMPredictor, error) {
	ens, err := leaves.LGEnsembleFromFile(path, true)
	if err != nil {
		return nil, err
	}
	return &LightGBMPredictor{ensemble: ens, name: path}, nil
}

// Predict runs inference on a single feature vector.
func (p *LightGBMPredictor) Predict(values []float64) (float64, error) {
	result := p.ensemble.PredictSingle(values, -1)
	return result, nil
}

// Name returns the model file path.
func (p *LightGBMPredictor) Name() string { return p.name }

// MockPredictor is a deterministic predictor for testing.
type MockPredictor struct {
	Value float64
}

func (m *MockPredictor) Predict(values []float64) (float64, error) {
	return m.Value, nil
}

func (m *MockPredictor) Name() string { return "mock" }

// viewFromMatchView converts a MatchView to a features.View.
// This is the bridge between the loop's state and the feature extractor.
func viewFromMatchView(mv MatchView, marketTicker string) features.View {
	price := mv.Prices[marketTicker]
	ts := mv.PriceTS[marketTicker]

	homePoints, _ := strconv.Atoi(mv.HomePoints)
	awayPoints, _ := strconv.Atoi(mv.AwayPoints)

	return features.View{
		SetsHome:     mv.SetsHome,
		SetsAway:     mv.SetsAway,
		GamesHome:    mv.GamesHome,
		GamesAway:    mv.GamesAway,
		HomePoints:   homePoints,
		AwayPoints:   awayPoints,
		Server:       mv.Server,
		IsTiebreak:   mv.IsTiebreak,
		SetNumber:    mv.SetNumber,
		GameNumber:   mv.GameNumber,
		PointNumber:  mv.PointNumber,
		IsBreakPoint: mv.IsBreakPoint,
		IsSetPoint:   mv.IsSetPoint,
		IsMatchPoint: mv.IsMatchPoint,
		PriceCents:   price,
		PriceTS:      ts,
	}
}
