package tracker

// MultiScorePoller fans out StartPolling/StopPolling to multiple ScorePoller
// implementations. Used to run API-Tennis (primary) and Kalshi live-data
// (backup) pollers simultaneously.
type MultiScorePoller struct {
	pollers []ScorePoller
}

// NewMultiScorePoller wraps multiple ScorePoller implementations.
func NewMultiScorePoller(pollers ...ScorePoller) *MultiScorePoller {
	return &MultiScorePoller{pollers: pollers}
}

func (m *MultiScorePoller) StartPolling(eventTicker string) {
	for _, p := range m.pollers {
		if p == nil {
			continue
		}
		p.StartPolling(eventTicker)
	}
}

func (m *MultiScorePoller) StopPolling(eventTicker string) {
	for _, p := range m.pollers {
		if p == nil {
			continue
		}
		p.StopPolling(eventTicker)
	}
}
