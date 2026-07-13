package ws

import "time"

const (
	// wsReadLimit caps incoming WS message size. Default 32KB too small
	// for ticker feeds with many markets.
	wsReadLimit = 1 << 20 // 1MB

	// secondsToMillis converts Kalshi lifecycle timestamps (seconds) to millis.
	secondsToMillis = 1000

	// subscribeAckTimeout caps how long Subscribe waits for server ack.
	subscribeAckTimeout = 5 * time.Second
)
