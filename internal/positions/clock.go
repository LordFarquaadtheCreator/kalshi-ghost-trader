package positions

import "time"

// timeNowMillis is the actual clock used by nowMillis in manager.go.
// Kept separate so tests can override nowMillis without touching time.
func timeNowMillis() int64 {
	return time.Now().UnixMilli()
}
