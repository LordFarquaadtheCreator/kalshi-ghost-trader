package orders

import "testing"

// TestLegalTransitions exhaustively verifies the legal-transition map.
func TestLegalTransitions(t *testing.T) {
	// Define expected transitions as data.
	expected := map[string][]string{
		StatusIntent:    {StatusGated, StatusAccepted},
		StatusGated:     {},
		StatusAccepted:  {StatusHeld, StatusFilled, StatusGated},
		StatusHeld:      {StatusSubmitted, StatusFailed},
		StatusSubmitted: {StatusFilled, StatusPartial, StatusCanceled, StatusUnverified, StatusFailed},
		StatusPartial:   {StatusFilled, StatusCanceled, StatusSettled},
		StatusUnverified: {StatusFilled, StatusPartial, StatusCanceled},
		StatusFilled:    {StatusSettled},
		StatusCanceled:  {},
		StatusFailed:    {},
		StatusSettled:   {},
	}

	for from, targets := range expected {
		for _, to := range targets {
			if !IsLegalTransition(from, to) {
				t.Errorf("IsLegalTransition(%s, %s) = false, want true", from, to)
			}
		}
	}

	// Verify illegal transitions.
	illegal := []struct{ from, to string }{
		{StatusIntent, StatusFilled},
		{StatusIntent, StatusHeld},
		{StatusGated, StatusAccepted},
		{StatusGated, StatusFilled},
		{StatusHeld, StatusFilled},
		{StatusHeld, StatusGated},
		{StatusSubmitted, StatusAccepted},
		{StatusSubmitted, StatusHeld},
		{StatusFilled, StatusCanceled},
		{StatusFilled, StatusSubmitted},
		{StatusCanceled, StatusFilled},
		{StatusFailed, StatusFilled},
		{StatusSettled, StatusFilled},
	}
	for _, tc := range illegal {
		if IsLegalTransition(tc.from, tc.to) {
			t.Errorf("IsLegalTransition(%s, %s) = true, want false", tc.from, tc.to)
		}
	}
}

// TestAllStatuses verifies every status is in the map.
func TestAllStatuses(t *testing.T) {
	all := AllStatuses()
	if len(all) != 11 {
		t.Errorf("AllStatuses returned %d, want 11", len(all))
	}
	for _, s := range all {
		if _, ok := legalTransitions[s]; !ok {
			t.Errorf("status %s not in legalTransitions map", s)
		}
	}
}
