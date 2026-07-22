package modelregistry

import (
	"testing"
)

func TestLegalTransitions(t *testing.T) {
	tests := []struct {
		from, to Status
		want     bool
	}{
		// Promotions — one rung at a time.
		{StatusCandidate, StatusShadow, true},
		{StatusShadow, StatusPaper, true},
		{StatusPaper, StatusChampion, true},

		// Skipped rungs — illegal.
		{StatusCandidate, StatusPaper, false},
		{StatusCandidate, StatusChampion, false},
		{StatusShadow, StatusChampion, false},

		// Demotions.
		{StatusPaper, StatusShadow, true}, // drift/drawdown demotion
		{StatusCandidate, StatusRetired, true},
		{StatusShadow, StatusRetired, true},
		{StatusPaper, StatusRetired, true},
		{StatusChampion, StatusRetired, true},

		// No backward from champion except retired.
		{StatusChampion, StatusPaper, false},
		{StatusChampion, StatusShadow, false},
		{StatusChampion, StatusCandidate, false},

		// Retired is terminal.
		{StatusRetired, StatusCandidate, false},
		{StatusRetired, StatusShadow, false},
		{StatusRetired, StatusPaper, false},
		{StatusRetired, StatusChampion, false},

		// No-op.
		{StatusCandidate, StatusCandidate, false},
		{StatusChampion, StatusChampion, false},
	}

	for _, tt := range tests {
		got := CanTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("CanTransition(%s→%s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestValidateTransitionErrors(t *testing.T) {
	// Skipped rung.
	err := ValidateTransition(StatusCandidate, StatusPaper)
	if err == nil {
		t.Error("expected error for candidate→paper (skipped shadow)")
	}

	// No-op.
	err = ValidateTransition(StatusShadow, StatusShadow)
	if err == nil {
		t.Error("expected error for no-op transition")
	}

	// Legal.
	err = ValidateTransition(StatusCandidate, StatusShadow)
	if err != nil {
		t.Errorf("unexpected error for candidate→shadow: %v", err)
	}
}

func TestStrategyName(t *testing.T) {
	m := Model{Family: FamilyFairValue, Version: 37}
	if got := m.StrategyName(); got != "rl.fairvalue.v37" {
		t.Errorf("StrategyName = %s, want rl.fairvalue.v37", got)
	}
}
