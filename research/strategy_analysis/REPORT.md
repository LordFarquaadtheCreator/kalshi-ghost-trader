# Strategy Analysis Report — Kalshi Tennis Match-Winner Markets

Generated from `kalshi_tennis.db` (live, read-only). 3 days of data
(Jul 13–16, 2026). 494 finalized events with both YES/NO markets, 89 gold-set
matches with point-by-point + tick overlap, 924k ticks, 34M orderbook events,
54k points. Challenger/ITF heavy.

All backtests: $100 fixed stake, 1c/share round-trip fee, read-only DB.
Modules in `research/strategy_analysis/`, raw JSON in `out/`.

## Headline: tradeable edges ranked by risk-adjusted return

| # | Strategy | n | Hit | Entry | Edge vs implied | Sharpe | Verdict |
|---|---|---|---|---|---|---|---|
| 1 | Fade longshot, buy favorite @ T-10min, price≥85c | 42 | 100% | 95.0c | +5.0c | 1.01 | **Best. Highest risk-adj.** |
| 2 | Fade longshot, buy favorite @ T-10min, price≥70c | 59 | 100% | 90.3c | +9.7c | 0.97 | **Trade. Bigger edge, wider.** |
| 3 | Fade longshot, buy favorite @ T-5min, price≥70c | 56 | 100% | 95.4c | +4.6c | 0.72 | **Trade. Smaller, tighter.** |
| 4 | Buy match-point player when SERVING for match | 37 | 97.3% | 91.3c | +6.0c | 0.23 | **Trade. Best in-play edge.** |
| 5 | Fade huge spikes (>30c in 30s), hold 60s | 61 | 65.6% | — | +5.75c | 0.44 | **Trade, small n.** |
| 6 | Buy match-point player when RETURNING (breaking) | 26 | 88.5% | 80.0c | +8.5c | 0.20 | Lottery. High variance. |
| 7 | Top-of-book imbalance >0.9 | 15886 | 20.4% | — | -0.76c net | -0.25 | **Do not trade.** |
| 8 | Fade regular 10-30c spikes | 3066 | 44% | — | -0.67c net | 0.03 | **Do not trade.** |
| 9 | Buy saver (comeback bet at match point) | 56 | 7.1% | 14.9c | -7.8c | -1.92 | **Do not trade.** |

## 1. Nothing Ever Happens — fade the longshot near close

`nothing_happens.py`. At T-Xmin before close, buy the higher-priced side
(the favorite). Settles $1 if they won, $0 if not.

**Best result — T-10min, threshold ≥85c**: n=42, 100% hit, entry 95.0c,
+5.0c edge, **Sharpe 1.01** (highest in the whole study). Entering 10min
out gives MORE edge than 5min (the market is even more under-priced
earlier) and tighter variance. T-10min threshold ≥70c: n=59, 100% hit,
+9.7c edge, Sharpe 0.97 — bigger edge but wider (entry 90.3c, one upset
hurts more).

**T-5min, threshold ≥70c**: n=56, 100% hit, entry 95.4c, +4.6c edge,
Sharpe 0.72. Smaller edge but very tight. The favorite at 5min out won
every single time in this dataset. Underdog contrast: 0% hit, -$8596
total — confirms the asymmetry.

**Tail risk**: at T-2min threshold 0 (no price filter), one upset wiped 52
wins (net -$166). The threshold filter (≥70c) removes uncertain matches
where the "favorite" was barely ahead. **Always filter: only enter when
favorite is clearly priced (≥70c, ideally ≥85c).**

**Cost check**: spread at T-5–10min is 0.44c (see §5). Edge comfortably
covers 0.44c spread + 1c fee. Enter 5–10min out, NOT T-0min (spread 3.86c).

**Statistical caveat**: n=42–59. If true win prob is 95%, P(59/59) = 4.9%.
If 97%, P = 16.6%. The edge is directionally real but the 100% is partly
small-sample luck. Expect upsets as n grows. Position size for the tail:
one loss at 95c entry = -$95 on $100 stake ≈ wipes 19 wins ($5 each).

## 2. Match-point edge — buy the finisher

`match_point_edge.py`. At each match point, buy the player-with-match-point's
YES, hold to settlement.

**Best variant — serving for match**: n=37, 97.3% hit, entry 91.3c, +6.0c
edge, Sharpe 0.23. When the MP player is serving (high conversion), market
under-prices by 6c.

**Returning (breaking for match)**: n=26, 88.5% hit, entry 80c, +8.5c edge
but Sharpe 0.20 — higher edge, higher variance. A few cheap entries (1-10c)
where market doubted the break dominate PnL. Lottery-like.

**Do NOT buy the saver**: opponent comeback at match point = 7.1% hit,
-$8193 total. Comebacks are rare and the market prices them correctly
(opponent YES at 14.9c avg, true prob 7.1% → market OVER-prices comebacks
slightly, but the strategy of buying them is catastrophic).

**Conversion rate**: 47/63 = 74.6% per match-point. Serving-self 73%,
returning 77% (small n). Note: hit_rate (97%) > conversion (75%) because
the contract settles at match end — a player with 3 MPs who converts the
3rd settles $1 on all 3 trades.

**Action**: the existing `orders` table logs a match-point strategy. The
data supports buying the MP player when serving. Current logged orders mix
buy/sell on away_match_point — clean up to: **buy server-MP player's YES,
skip returning MPs unless price < 50c (lottery ticket sizing).**

## 3. Spike reversion — only fade extreme moves

`spike_reversion.py`. 30s window, 60s hold.

**Regular spikes (10-30c)**: 3066 events, 5.7% reversion, fade avg +0.33c
< 1c fee → net loss. **Tennis price spikes are informational (break of
serve, point won), not noise. Do not fade them.**

**Huge spikes (>30c in 30s)**: n=61, 0% full reversion but 65.6% fade win
rate, +5.75c avg, Sharpe 0.44. These are overreactions (often match-point
saves or double breaks) that partially drift back. Small n — treat as
opportunistic, not systematic.

## 4. Orderbook imbalance — real signal, too weak to trade

`orderbook_imbalance.py`. Top-of-book (yes_bid_size vs yes_ask_size),
30s forward price move.

**Monotonic**: D0 (heavy ask) -0.25c → D9 (heavy bid) +0.25c. Pearson
corr 0.054. **Signal is real but max 0.25c/trade << 1c fee.** Every
threshold backtest net-negative. Matches the Wilkens/concerns.md warning:
market microstructure re-extracts info already in price. Use as a feature
in the momentum-alpha ML model, not as a standalone strategy.

## 5. Spread & liquidity — when to enter

`spread_liquidity.py`. 836k spread samples.

- Median spread 0c (locked/crossed top common on Kalshi). Mean 0.78c.
- **Stable 0.44c from 10min → 1min before close. Jumps to 3.86c at 0min.**
  Final-minute liquidity drain. Enter 5-10min out, never in the last 60s.
- **Cheap contracts (<10c) cost 10.6% of price in spread.** Longshot
  trading is expensive in % terms. The fade-longshot strategy buys 90c+
  contracts (0.6% spread) — cheap to trade. Buying underdogs at 5c costs
  10%+ in spread alone — another reason saver/comeback bets lose.

## Strategy recommendations

### Ship now (validated directionally, small n)
1. **Fade longshot @ T-10min, favorite ≥85c**: enter 10min before close,
   buy the side priced ≥85c, hold to settlement. Sharpe 1.01, +5c edge,
   100% hit in sample. Tightest risk-adjusted entry. Cap position so one
   loss doesn't wipe a day's gains (~19:1 win:loss size at 95c entry).
   Also run the ≥70c variant (Sharpe 0.97, +9.7c edge) with smaller size
   since one upset at 90c entry = -$90.
2. **Buy server match-point**: when a player reaches match point while
   serving, buy their YES. +6c edge, 97% hit. Skip returning MPs unless
   price < 50c (treat as small lottery tickets).

### Investigate further
3. **Huge-spike fade (>30c)**: more data needed (n=61). Backtest with
   tighter entry (only fade spikes that coincide with non-match-point
   context — match-point spikes are informational and won't revert).
4. **Orderbook imbalance as ML feature**: feed into momentum-alpha
   pipeline alongside court features. Don't trade standalone.

### Do not ship
5. Fade regular spikes — net negative after fees.
6. Buy saver/comeback at match point — catastrophic.
7. Imbalance threshold strategies — net negative after fees.

## Open concerns (from concerns.md, still valid)

- **Sample size**: 56-89 trades per strategy. Need 200+ before trusting
  Sharpe. Direction is clear; magnitude is uncertain.
- **No walk-forward**: current backtest is in-sample on all finalized
  events. Re-run with time-split (train Jul 13-14, test Jul 15-16) once
  more data lands.
- **Fee model**: assumed 1c/share round-trip. Verify against actual
  Kalshi fee schedule + slippage on real fills.
- **Selection bias**: only 57/494 events had ticks at T-5min. The other
  437 closed without trailing liquidity — are those different matches
  (more decisive?) or just timing gaps? Investigate.
- **Market efficiency risk**: favorite edge = market under-pricing
  near-certainty. If smart money arb this, edge decays. Monitor live.

## Reproduce

```bash
cd research/strategy_analysis
python3 run_all.py        # runs all 6 modules, writes out/*.json
cat out/overview.json     # dataset summary
cat REPORT.md             # this file
```

All modules open the DB read-only (`?mode=ro`). Safe to run while the
scraper writes. Outputs in `out/`.
