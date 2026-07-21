# Exploratory Research Questions + Preliminary Findings

Fresh angles not covered by `strategy_analysis/REPORT.md` (which already
nailed: fade-longshot, match-point edge, spike reversion, top-of-book
imbalance, spread/liquidity).

Dataset: 4 days Jul 13-17 2026. 861 tracked events (16.9k orphan event
rows = scanner noise, no markets). 1.26M ticks, 43.5M orderbook deltas,
6.6k orderbook snapshots, 56k points (singles only), 114 gold-set matches
with both points + ticks.

---

## RQ1. Latency arbitrage — is our data delay exploitable?

**Question**: recv_ts - ts gap. Where does it concentrate? Does delay
spike at match-critical moments (break points, match points) when edge
is largest?

**Preliminary**:
- 80% of ticks <500ms, 14% 500-1000ms, 1.5% 1-5s, 0.7% 5-30s, 0.3% >30s.
- Max 486s (8min) — likely reconnect gap.
- Median ~200ms. Mean 827ms (skewed by tail).

**Hypothesis**: Kalshi WS delivers in <500ms normally. Tail latency =
reconnects. Edge-killing moments (break points) may coincide with
latency spikes if our WS backpressures. Test: bucket latency by
simultaneous point events from `points` table.

**Next**: correlate recv_ts-ts with `points.ts_ms` for gold-set matches.
Are we slower during break points?

---

## RQ2. YES/NO cross-side arbitrage — does the pair ever misprice?

**Question**: each event has 2 markets (player A YES, player B YES).
`yes_ask_A + yes_ask_B < 1` = buy both, lock profit. `yes_bid_A +
yes_bid_B > 1` = sell both. How often, how big, after fees?

**Preliminary** (189k paired observations, both sides quoted >1c):
- avg ask sum 1.025 (2.5% premium to buy both sides)
- avg bid sum 0.977 (2.3% discount to sell both)
- ask_arb (<0.99): 397 obs (0.21%)
- bid_arb (>1.01): 706 obs (0.37%)

**Hypothesis**: structural premium means market makers earn ~2.5% spread
on round-trips. True arbs are rare and small — likely closed by faster
bots before we'd see them. The 397/706 are probably stale-quote
artifacts (one side updated before the other in our snapshot).

**Next**: for each arb candidate, check if both quotes persisted >2s
(real arb) or vanished next tick (stale). Compute net edge after 1c
fee + 1.75% taker fee.

---

## RQ3. Series tier efficiency — do lower-tier markets misprice more?

**Question**: ATP main tour vs Challenger vs ITF. Different participant
mix (more retail on ITF?). Does edge concentrate in lower tiers?

**Preliminary** (markets per series):
- KXITFWMATCH 480, KXATPCHALLENGERMATCH 358, KXITFMATCH 310,
  KXWTACHALLENGERMATCH 180, KXATPMATCH 166, KXWTAMATCH 136,
  KXITFDOUBLES 50, KXITFWDOUBLES 42.

**Hypothesis**: ITF/Challenger have thinner liquidity, wider spreads,
slower price discovery → more edge. ATP main tour most efficient.

**Next**: re-run `nothing_happens.py` and `match_point_edge.py` per
series tier. Compare Sharpe across tiers.

---

## RQ4. Taker flow toxicity — does trade flow predict price?

**Question**: `taker_side` + `taker_book_side` give signed flow. Buy-YES
(hit ask) vs sell-YES (hit bid). Does flow imbalance over 30/60s predict
forward price move? VPIN-style toxicity.

**Preliminary**:
- 462k trades sell YES (taker_side=yes, book=bid) = bearish on YES player
- 216k trades buy NO (taker_side=no, book=ask) = bearish on YES player
- Both directions are bearish on the market's YES player (makes sense:
  selling YES and buying NO are equivalent).

**Hypothesis**: net flow imbalance (buy-YES vs sell-YES on same market)
predicts short-term price drift. Existing `orderbook_imbalance.py`
showed top-of-book imbalance too weak (0.25c). Trade flow may be
stronger because it's committed capital, not quote bluffing.

**Next**: per-market, 60s rolling signed volume delta. Correlate with
60s forward mid-price move. Compare to orderbook imbalance signal
strength.

---

## RQ5. Settlement timing — can we predict the delay?

**Question**: close_ts → settlement_ts gap. Avg 3.2min, median 2min,
max 53min. 36 matches took 15-60min. What predicts slow settlement?

**Preliminary**:
- 88% settle in 2-5min, 9% <2min, 1% 5-15min, 3% 15-60min.
- No series tier breakdown yet (need to recompute — earlier query had
  unit bug).

**Hypothesis**: slow settlement = disputed final score, doubles matches
(more complex scoring), or matches needing manual review. Long delay =
capital locked longer = should demand higher edge.

**Next**: correlate settlement delay with series, doubles vs singles,
match length (total points), final score closeness (3 sets vs 2).

---

## RQ6. Empirical serve hold rates — does our Markov model match reality?

**Question**: Markov model assumes a serve-win probability. What's the
empirical hold rate by series? By break-point score state? Does the
model's assumption match?

**Preliminary** (singles only, points table):
- ATP main: 61.3% hold, 33.5% break-point hold
- ATP Challenger: 61.0%, 31.7%
- ITF men: 57.9%, 30.3%
- ITF women: 54.0%, 27.3%
- WTA Challenger: 52.3%, 29.7%
- WTA main: 52.0%, 27.3%

Break point by score (server perspective):
- 0-40: 0% hold (server always broken — only 1 chance needed)
- 15-40: 13.5% hold
- 30-40: 24.4% hold
- 40-40 (deuce): 49.7% hold (NOT a real break point but flagged)

**Hypothesis**: ATP hold 61% is LOW vs tour average 65-80%. Either
our data skews to clay/lower tournaments, or break-point conversion
is higher at lower tiers. Markov model likely overestimates hold on
break points → overestimates server's match-win prob at break-down.

**Next**: surface extraction from `flashscore_matches.surface`. Recompute
hold rate by surface. Compare Markov model output to market price at
break-down states.

---

## RQ7. Break-point over-reaction — does the Betfair pattern hold on Kalshi?

**Question**: Betfair literature says markets OVER-react to breaks and
UNDER-react to break-backs. Does Kalshi show the same? Fade the breaker
after conversion, back the favourite at 0-40 down.

**Preliminary**: not yet computed. Need to align `points.is_break_point`
+ `points.scorer` (break converted) with tick price moves in the
±60s window around the point.

**Hypothesis**: Kalshi has more retail, less sharp money → over-reaction
STRONGER than Betfair. But thinner liquidity = harder to fill fade.

**Next**: for each break conversion in gold set, measure price move in
30s before and 60s after. Test fade-the-breaker (sell breaker's YES)
held 60-120s. Compare to `spike_reversion.py` but filtered to
break-context only.

---

## RQ8. Match-point calibration — is the market efficient at match point?

**Question**: at each match point, market price implies a conversion
prob. Empirical conversion rate? Calibration curve. Where does market
misprice — serving vs returning MPs?

**Preliminary**: `is_match_point` NOT populated in points table (all 0).
Strategy computes live. Need to recompute match-point flag from
score state (set/game/point + best-of format).

**Hypothesis**: existing REPORT.md found serving-MP edge +6c, returning
+8.5c. Market under-prices both. But n=37/26. With 114 gold-set matches
we can recompute MPs properly and triple n.

**Next**: write match-point classifier from `points` (set/game/point +
tennis format rules). Join with tick price at point timestamp. Build
calibration curve: implied prob bucket vs empirical conversion.

---

## RQ9. Orderbook depth dynamics — multi-level signal?

**Question**: existing study used top-of-book only. Snapshots have full
depth ladder (`yes_dollars_fp` with 10+ levels). Does depth at level 2-5
predict volatility better than top-of-book?

**Preliminary**: 6.6k snapshots, 43.5M deltas. Snapshot payload has
full ladder. Can reconstruct depth at any ts.

**Hypothesis**: thin depth at levels 2-3 = fragile quote = next price
jump larger. Top-of-book imbalance misses this.

**Next**: parse snapshots, compute depth profile (cumulative size at
5 levels each side). Correlate depth asymmetry with 60s forward
volatility (not just price move).

---

## RQ10. Time-of-day efficiency — when are markets most inefficient?

**Question**: matches happen across UTC hours (ITF Europe daytime, US
evening). Does edge concentrate when US retail is active (UTC 16-04)?

**Preliminary**: tick volume by UTC hour peaks 18-23 UTC (US evening).
Low 06-10 UTC. Need to recompute spread/edge by hour (earlier query
had precision bug).

**Hypothesis**: US evening = more retail = wider spreads but more
mispricing. European daytime = sharper, tighter.

**Next**: re-run `nothing_happens.py` and `match_point_edge.py`
stratified by UTC hour of close_ts. Compare Sharpe.

---

## RQ11. Comeback empirical frequency — does the market price comebacks right?

**Question**: how often does a player down 0-40 come back to hold?
Down a set? Down 2 sets? Market price vs empirical. Existing REPORT.md
found buying the saver at match point = catastrophic (7.1% hit, market
prices at 14.9c → market OVER-prices comebacks). Confirm with larger n.

**Preliminary**: 0-40 hold = 0% (server always broken — only 1 chance
to break, returner needs 1 of 1). Wait, that's wrong. 0-40 means
returner needs 1 point to break. Server needs 4 points to hold. So
server hold from 0-40 = ~0% makes sense (need 4 consecutive points).
But "comeback" here = hold from 0-40, which is rare.

**Hypothesis**: market OVER-prices comebacks at match point (saver
strategy loses). Under-prices comebacks at 0-40 on serve (no one
expects hold from 0-40, but it happens ~2% of the time — if market
prices at 0.5c but true is 2%, that's 4x).

**Next**: for each 0-40, 15-40, 30-40 state, compute empirical hold
rate. Compare to market YES price of server at that moment. Find
states where market diverges most from empirical.

---

## RQ12. Point-to-price latency — how fast does Kalshi react to a point?

**Question**: from `points.ts_ms` (point scored) to next tick price
change. Distribution. Does Kalshi price move before our point feed
catches up (would mean Kalshi has faster score source)?

**Preliminary**: not yet computed. Gold set = 114 matches with both.

**Hypothesis**: Kalshi market makers have faster score feeds
(Sportradar 0.5-1.5s vs our API-Tennis 5-15s). Price moves BEFORE
our point timestamp. If so, our edge is gone the moment we see the
point — we're the exit liquidity.

**Next**: for each gold-set point, find nearest tick within ±30s.
Compute (tick_ts - point_ts). If most price moves happen BEFORE
point_ts → we're late. If after → we have a window.

---

## RQ13. Doubles blind spot — are we ignoring a market?

**Question**: 50 KXITFDOUBLES + 42 KXITFWDOUBLES markets tracked. Zero
points data for doubles. Doubles have different hold rates (lower,
more breaks), different dynamics. Are we leaving edge unmodeled?

**Preliminary**: points table = singles only. Doubles markets have
ticks but no score feed. Orders table shows 4 doubles orders
(server1530, breakback, tiebreak strategies).

**Hypothesis**: doubles markets less efficient (less sharp money),
wider spreads, but we have no point data to model them. Either
integrate doubles score feed or accept we can only trade pre-match
edges there.

**Next**: check if API-Tennis supports doubles. If yes, extend
scraper. If no, doubles = pre-match-only (fade-longshot strategy
applies).

---

## RQ14. Volume / OI as outcome predictor

**Question**: does cumulative dollar_volume or open_interest at
T-10min predict match outcome? Heavy money on one side = sharp
signal?

**Preliminary**: dollar_volume is cumulative, most mature markets
>$100k. Need to compute per-side volume (YES vs NO market of same
event) and compare to outcome.

**Hypothesis**: if YES market A has 3x dollar_volume of YES market B,
sharp money favors A. Does A win more often than price implies?

**Next**: for each event, ratio of dollar_volume between the two
player-markets at close. Correlate with result. Compare to final
price-implied prob.

---

## RQ15. Adverse selection — when does the spread widen?

**Question**: spread widens before price jumps (informed traders
arrive). Can we detect spread-widening as a signal to NOT trade
(or to trade the other way)?

**Preliminary**: 54% of ticks have locked spread (bid=ask). Mean
spread 0.78c. Existing `spread_liquidity.py` showed spread jumps
3.86c at 0min before close.

**Hypothesis**: spread widening at non-close moments = informed flow
imminent. Tight spread = safe to take liquidity.

**Next**: per-market, track spread timeseries. Flag moments where
spread jumps >2x rolling 5min avg. Measure forward 60s price move
magnitude vs control (normal spread periods).

---

## Priority ranking

**High (edge-direct, uses existing data)**:
- RQ7 break-point over-reaction (Betfair pattern test)
- RQ8 match-point calibration (recompute properly, triple n)
- RQ12 point-to-price latency (determines if we have any edge at all)
- RQ4 taker flow toxicity (signed flow vs forward price)

**Medium (model quality)**:
- RQ6 empirical serve hold vs Markov assumption
- RQ11 comeback empirical vs market price
- RQ9 orderbook depth dynamics

**Low (structural / context)**:
- RQ1 latency arbitrage
- RQ2 YES/NO cross-side arb
- RQ3 series tier efficiency
- RQ5 settlement timing
- RQ10 time-of-day efficiency
- RQ13 doubles blind spot
- RQ14 volume/OI predictor
- RQ15 adverse selection

---

## Reproduce

All queries in this doc run against PostgreSQL `kalshi_tennis` read-only.
Modules to write (in `research/strategy_analysis/`):
- `breakpoint_overreaction.py` (RQ7)
- `matchpoint_calibration.py` (RQ8)
- `point_price_latency.py` (RQ12)
- `taker_flow.py` (RQ4)
- `empirical_hold.py` (RQ6, RQ11)
- `depth_dynamics.py` (RQ9)
