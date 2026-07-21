# Perps Price Banding

> How price banding works for Kalshi margin markets

For perpetual markets, prices move in `0.0001` dollar ticks.

- **Bids**: must be at least the lower of 80% of the best bid or 1,000 ticks below the best bid
- **Asks**: must be at most the higher of 120% of the best ask or 1,000 ticks above the best ask

**Notes:**
- Resting orders not canceled due to price band movement
- If no resting orders on a side, no band limit for that side
- Order amends outside the price band are not allowed
