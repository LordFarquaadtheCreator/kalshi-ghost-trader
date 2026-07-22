package strategy

import (
	"testing"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
)

func TestBuyTheDipScenarios(t *testing.T) {
	tests := []struct {
		name    string
		mv      MatchView
		events  []match.Event
		wantLen int
		check   func(t *testing.T, intents []match.Intent)
	}{
		{
			name: "dip_detected_buy",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 35, TS: 2000},
			},
			wantLen: 1,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[0].Action != "buy" {
					t.Errorf("Action = %s, want buy", intents[0].Action)
				}
				if intents[0].MarketTicker != "E1-H" {
					t.Errorf("MarketTicker = %s, want E1-H", intents[0].MarketTicker)
				}
				if intents[0].PriceCents != 35 {
					t.Errorf("PriceCents = %d, want 35", intents[0].PriceCents)
				}
				if intents[0].ConvProbBps != 7000 {
					t.Errorf("ConvProbBps = %d, want 7000", intents[0].ConvProbBps)
				}
			},
		},
		{
			name: "no_dip_small_drop",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 60, TS: 2000},
			},
			wantLen: 0,
		},
		{
			name: "match_point_excluded",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
				IsMatchPoint:  true,
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 35, TS: 2000},
			},
			wantLen: 0,
		},
		{
			name: "tp_exit_sell",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 35, TS: 2000},  // buy, dip=35
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 65, TS: 3000},  // tp=35+26=61, 65>=61
			},
			wantLen: 2,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[0].Action != "buy" {
					t.Errorf("intent[0] Action = %s, want buy", intents[0].Action)
				}
				if intents[1].Action != "sell" {
					t.Errorf("intent[1] Action = %s, want sell", intents[1].Action)
				}
				if intents[1].Reason != "btd_sell_tp" {
					t.Errorf("intent[1] Reason = %s, want btd_sell_tp", intents[1].Reason)
				}
			},
		},
		{
			name: "sl_exit_sell",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 35, TS: 2000},  // buy, dip=35
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 20, TS: 3000},  // sl=35-10=25, 20<=25
			},
			wantLen: 2,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[1].Action != "sell" {
					t.Errorf("intent[1] Action = %s, want sell", intents[1].Action)
				}
				if intents[1].Reason != "btd_sell_sl" {
					t.Errorf("intent[1] Reason = %s, want btd_sell_sl", intents[1].Reason)
				}
			},
		},
		{
			name: "time_exit_sell",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 70, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 35, TS: 2000},   // buy
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 40, TS: 303000}, // time: 303000-2000=301000>=300000
			},
			wantLen: 2,
			check: func(t *testing.T, intents []match.Intent) {
				if intents[1].Reason != "btd_sell_time" {
					t.Errorf("intent[1] Reason = %s, want btd_sell_time", intents[1].Reason)
				}
				if intents[1].PriceCents != 40 {
					t.Errorf("intent[1] PriceCents = %d, want 40 (current price)", intents[1].PriceCents)
				}
			},
		},
		{
			name: "pre_dip_too_low",
			mv: MatchView{
				EventTicker:   "E1",
				MarketTickers: []string{"E1-H", "E1-A"},
				Prices:        map[string]int{},
				PriceTS:       map[string]int64{},
			},
			events: []match.Event{
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 45, TS: 1000},
				match.PriceUpdate{MarketTicker: "E1-H", PriceCents: 10, TS: 2000}, // dip=35 but pre-dip 45 < 50
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewBuyTheDip()
			st := &State{
				MatchView:     tt.mv,
				StrategyState: map[string]any{},
			}
			var allIntents []match.Intent
			for _, ev := range tt.events {
				intents := s.OnEvent(ev, st)
				allIntents = append(allIntents, intents...)
			}
			if len(allIntents) != tt.wantLen {
				t.Fatalf("got %d intents, want %d", len(allIntents), tt.wantLen)
			}
			if tt.check != nil && len(allIntents) > 0 {
				tt.check(t, allIntents)
			}
		})
	}
}
