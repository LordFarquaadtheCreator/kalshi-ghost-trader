package httpapi

import (
	"fmt"
	"strconv"
	"strings"
)

// parseCursorPairImpl parses "ts,id" cursor format into int64 values.
func parseCursorPairImpl(s string, ts, id *int64) (int, error) {
	parts := strings.SplitN(s, ",", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid cursor format")
	}
	tsVal, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	idVal, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, err
	}
	*ts = tsVal
	*id = idVal
	return 2, nil
}

// buildCursor creates a "ts,id" cursor from row values.
func buildCursor(tsVal, idVal any) string {
	return fmt.Sprintf("%v,%v", tsVal, idVal)
}
