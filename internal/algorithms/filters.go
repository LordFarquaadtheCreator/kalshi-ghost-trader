package algorithms

import "time"

// seriesMatches returns true if series is in filter, or if filter is empty
// (no filter = match all). Used by SeriesFilter config fields.
func seriesMatches(series string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, s := range filter {
		if series == s {
			return true
		}
	}
	return false
}

// utcHourMatches returns true if the UTC hour of ts falls in [start, end).
// Handles wraparound (e.g. start=18, end=4 = UTC 18-04).
// Both start and end 0 = no filter (always true).
func utcHourMatches(ts time.Time, start, end int) bool {
	if start == 0 && end == 0 {
		return true
	}
	h := ts.UTC().Hour()
	e := end
	if e == 0 {
		e = 24
	}
	if start <= e {
		return h >= start && h < e
	}
	return h >= start || h < e
}
