# Strategy Backtest Results — All Strategies

Run against `kalshi_tennis.db` (Jul 13-17 2026, 4 days, 864 matches with
markets, 1.29M tick prices, 56k points across 470 matches).

```bash
go run ./cmd/backtest -strategy all
```

## Results ranked by Sharpe

| # | Strategy | RQ | n | Hit | Net P&L | ROI | Sharpe | PF | Verdict |
|---|---|---|---|---|---|---|---|---|---|
| 1 | nofade | — | 34 | 100% | +$73.80 | +27.7% | 1.48 | — | **Best. Pre-match fade.** |
| 2 | fadelongshot | — | 54 | 100% | +$96.10 | +21.6% | 1.14 | — | **Trade. 100% hit.** |
| 3 | comeback040 | RQ11 | 25 | 28.0% | +$11.50 | +19.7% | 0.11 | 1.29 | **Promising. Positive.** |
| 4 | setdown | — | 73 | 47.9% | +$36.10 | +11.5% | 0.10 | 1.22 | **Trade. Set-loss recovery.** |
| 5 | convexpool | — | 5947 | 25.8% | +$1445.60 | +1.2% | 0.01 | 1.02 | Marginal. Huge n, tiny edge. |
| 6 | server1530 | — | 105 | 54.3% | -$4.10 | -0.7% | -0.01 | 0.98 | Break-even. 15-30 dip. |
| 7 | breakpoint | RQ7 | 347 | 27.7% | -$628.20 | -7.3% | -0.05 | 0.89 | **Do not trade.** |
| 8 | breakback | RQ7 | 131 | 31.3% | -$76.60 | -15.7% | -0.13 | 0.76 | **Do not trade.** |
| 9 | tiebreak | — | 93 | 35.5% | -$61.40 | -15.7% | -0.14 | 0.76 | **Do not trade.** |
| 10 | setpoint-cheap | RQ8 | 16 | 31.2% | -$113.00 | -18.4% | -0.14 | 0.74 | **Do not trade.** |
| 11 | matchpoint | RQ8 | 14 | 50.0% | -$267.20 | -33.9% | -0.46 | 0.34 | **Do not trade.** |
| 12 | matchpoint-aggro | RQ8 | 21 | 52.4% | -$327.00 | -28.3% | -0.36 | 0.44 | **Do not trade.** |
| 13 | setpoint | RQ8 | 77 | 54.5% | -$849.20 | -19.8% | -0.25 | 0.56 | **Do not trade.** |
| 14 | setpoint-serve | RQ8 | 44 | 45.5% | -$846.00 | -33.1% | -0.46 | 0.36 | **Do not trade.** |

## RQ-mapped findings

### RQ7 — Break-point over-reaction: NOT tradeable

- **breakback** (buy broken player): 131 trades, 31.3% hit, -$76.60, Sharpe -0.13.
- **breakpoint** (buy returner at BP): 347 trades, 27.7% hit, -$628.20, Sharpe -0.05.

Both negative. The Betfair "fade the breaker" pattern does NOT hold on Kalshi.
Breaks are informational here, not over-reactions. Market prices breaks correctly.

### RQ8 — Match-point calibration: market is EFFICIENT at match point

- **matchpoint**: 14 trades, 50% hit, -$267, Sharpe -0.46.
- **matchpoint-aggro**: 21 trades, 52.4% hit, -$327, Sharpe -0.36.
- **setpoint**: 77 trades, 54.5% hit, -$849, Sharpe -0.25.
- **setpoint-serve**: 44 trades, 45.5% hit, -$846, Sharpe -0.46.
- **setpoint-cheap**: 16 trades, 31.2% hit, -$113, Sharpe -0.14.

ALL negative. The earlier REPORT.md finding (+6c edge at serving MP) does not
hold with more data. Market is efficient at match/set points. The 97% hit rate
was small-sample luck (n=37). With n=14-77, hit rates are 31-54%.

### RQ11 — Comeback at 0-40: PROMISING

- **comeback040**: 25 trades, 28% hit, +$11.50, +19.7% ROI, Sharpe 0.11, PF 1.29.

Only positive in-play strategy besides convexpool. Buys server's YES at 0-40
down. 28% hit rate is low but the cheap entries (avg price ~0.20) mean wins
pay 5x. Small n — need 100+ to trust. But directionally positive.

### Pre-match strategies still dominate

- **nofade**: 34 trades, 100% hit, +$73.80, Sharpe 1.48. Best risk-adjusted.
- **fadelongshot**: 54 trades, 100% hit, +$96.10, Sharpe 1.14.

Both are pre-match fade-longshot variants. 100% hit rate in sample. The edge
is real but small-sample risk remains (one upset = -$90 at 90c entry).

### setdown — set-loss recovery

- 73 trades, 47.9% hit, +$36.10, Sharpe 0.10, PF 1.22.

Buy favourite after they lose set 1 (price drops to 0.30-0.45). Positive.
Consistent with Betfair literature (favourites recover 52-62% after set loss).

## What changed from REPORT.md

REPORT.md (3 days, 89 gold matches) said:
- Match-point serving: +6c edge, Sharpe 0.23 → **NOW negative** (Sharpe -0.46)
- Match-point returning: +8.5c edge, Sharpe 0.20 → **NOW negative** (Sharpe -0.36)

With 4 days and more matches, the match-point edge VANISHED. The earlier
finding was small-sample luck. Market is efficient at match point.

## Actionable

**Ship (validated directionally)**:
1. `nofade` / `fadelongshot` — pre-match fade. 100% hit, Sharpe 1.1-1.5.
2. `setdown` — set-loss recovery. Positive, PF 1.22.
3. `comeback040` — 0-40 comeback. Positive but small n (25). Monitor.

**Do not ship**:
4. All match-point / set-point strategies. Market efficient. Negative Sharpe.
5. breakback / breakpoint. Breaks informational, not over-reaction.
6. tiebreak. Mini-break fade negative.

**Investigate**:
7. `convexpool` — 5947 trades, +$1445, but Sharpe 0.01. Volume play?
   Needs fee analysis. After 1c/trade fee on 5947 trades = -$5947. Net negative.
