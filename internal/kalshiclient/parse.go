package kalshiclient

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"time"
)

// ParseTennisCompetitor extracts the tennis_competitor UUID from custom_strike.
func ParseTennisCompetitor(cs json.RawMessage) string {
	if len(cs) == 0 || string(cs) == "null" {
		return ""
	}
	var t CustomStrikeTennis
	if err := json.Unmarshal(cs, &t); err != nil {
		return ""
	}
	return t.TennisCompetitor
}

// ParseISOTime converts an ISO-8601 timestamp string to unix millis.
// Handles both RFC3339 (no fractional) and RFC3339Nano (with fractional seconds)
// since Kalshi returns both formats: "2026-07-13T14:30:00Z" and
// "2026-07-12T17:37:19.498576Z". Returns 0 on parse failure; logs via `log`.
func ParseISOTime(s string, log *slog.Logger) int64 {
	if s == "" {
		return 0
	}
	// Try RFC3339Nano first (handles fractional seconds)
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fall back to RFC3339 (no fractional)
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			if log != nil {
				log.Warn("failed to parse timestamp", "input", s, "err", err)
			}
			return 0
		}
	}
	return t.UnixMilli()
}

// ParseFP converts a fixed-point string to float64.
func ParseFP(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
