package httpapi

import "strconv"

// parseInt64Impl parses a string into an int64.
func parseInt64Impl(s string, n *int64) (int, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	*n = v
	return 0, err
}
