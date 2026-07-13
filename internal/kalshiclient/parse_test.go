package kalshiclient

import (
	"encoding/json"
	"testing"
)

func TestParseISOTime_RFC3339(t *testing.T) {
	got := ParseISOTime("2026-07-13T14:30:00Z", nil)
	if got == 0 {
		t.Fatal("expected non-zero for valid RFC3339")
	}
}

func TestParseISOTime_RFC3339Nano(t *testing.T) {
	got := ParseISOTime("2026-07-12T17:37:19.498576Z", nil)
	if got == 0 {
		t.Fatal("expected non-zero for valid RFC3339Nano")
	}
}

func TestParseISOTime_Empty(t *testing.T) {
	got := ParseISOTime("", nil)
	if got != 0 {
		t.Fatalf("empty string = %d, want 0", got)
	}
}

func TestParseISOTime_Invalid(t *testing.T) {
	got := ParseISOTime("not-a-timestamp", nil)
	if got != 0 {
		t.Fatalf("invalid = %d, want 0", got)
	}
}

func TestParseISOTime_ConsistentBetweenFormats(t *testing.T) {
	noFrac := ParseISOTime("2026-07-13T14:30:00Z", nil)
	withFrac := ParseISOTime("2026-07-13T14:30:00.000000Z", nil)
	if noFrac != withFrac {
		t.Fatalf("noFrac=%d withFrac=%d — should match", noFrac, withFrac)
	}
}

func TestParseFP(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"0.50", 0.50},
		{"1.0", 1.0},
		{"", 0},
		{"0", 0},
		{"99.99", 99.99},
	}
	for _, c := range cases {
		got := ParseFP(c.in)
		if got != c.want {
			t.Fatalf("ParseFP(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseFP_Invalid(t *testing.T) {
	got := ParseFP("not-a-number")
	if got != 0 {
		t.Fatalf("invalid = %v, want 0", got)
	}
}

func TestParseTennisCompetitor(t *testing.T) {
	// Valid object
	cs := json.RawMessage(`{"tennis_competitor":"abc-123-uuid"}`)
	got := ParseTennisCompetitor(cs)
	if got != "abc-123-uuid" {
		t.Fatalf("got %q, want %q", got, "abc-123-uuid")
	}
}

func TestParseTennisCompetitor_Null(t *testing.T) {
	got := ParseTennisCompetitor(json.RawMessage("null"))
	if got != "" {
		t.Fatalf("null = %q, want empty", got)
	}
}

func TestParseTennisCompetitor_Empty(t *testing.T) {
	got := ParseTennisCompetitor(nil)
	if got != "" {
		t.Fatalf("nil = %q, want empty", got)
	}
}

func TestParseTennisCompetitor_Invalid(t *testing.T) {
	got := ParseTennisCompetitor(json.RawMessage("not json"))
	if got != "" {
		t.Fatalf("invalid = %q, want empty", got)
	}
}
