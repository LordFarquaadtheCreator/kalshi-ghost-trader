# Deep Research 2: Trend Mining + Strategy Sharpening

Run against PostgreSQL `kalshi_tennis` (Jul 12-22 2026, 10 days, 20k events,
11k finalized markets, 6M ticks, 92k points, 742k paper orders).

## Methodology

5 parallel research tracks:
1. **Stratification** (RQ3+RQ10): strategy x series x UTC hour edge pockets
2. **Taker flow toxicity** (RQ4): signed trade flow vs forward price
3. **Orderbook depth dynamics** (RQ9): depth imbalance vs forward price
4. **Volume/OI predictor** (RQ14): pre-match volume ratio vs outcome
5. **Sharpen winners**: filter conditions that boost existing strategy Sharpe

Then implemented 7 filtered strategy variants, ran through backtest engine.

Scripts in `temp-scripts/`, outputs in `temp-scripts/*_out.csv`.

---

## Track 1: Stratification (RQ3 + RQ10)

### Top edge pockets by Sharpe (n>=30)

| # | Strategy | Filter | n | Sharpe | Net P&L | Note |
|---|---|---|---|---|---|---|
| 1 | tiebreak | KXITFWDOUBLES | 451 | 2.07 | +$1536 | Doubles tiebreak structure mispriced |
| 2 | tiebreak | UTC hour 1 | 1843 | 2.05 | +$2806 | Late-night/Asian session |
| 3 | setdown | UTC hour 12 | 2788 | 1.17 | +$8521 | Noon UTC peak |
| 4 | cross-arb-favorite | KXITFMATCH | 47 | 1.18 | +$676 | ITF men's favorites mispriced |
| 5 | server1530 | UTC hour 12 | 341 | 0.93 | +$1619 | Noon UTC |

### Series tier flips

| Strategy | Positive series | Negative series | Spread |
|---|---|---|---|
| breakback | ATP Challenger (+$33k) | ITF men's (-$60k) | $93k |
| setdown | ATP Challenger (+$21k) | ITF men's (-$14k) | $35k |
| tiebreak | WTA (+$3.6k) | ATP (-$5.5k) | $9k |
| server1530 | ATP Challenger (+$3.4k) | ITF men's (-$3k) | $6.4k |
| convexpool | WTA (+$4.3k) | ITF women's (-$584) | $4.9k |

**ITF men's (KXITFMATCH) is a graveyard** for breakback, setdown, server1530.

### UTC hour patterns

**European daytime (UTC 10-16) dominates** for nearly every strategy.
US evening (UTC 18-23) destroys expectancy for tiebreak, breakback, setpoint family.

- tiebreak: EU day +$12k (Sharpe 0.37) vs US evening -$5.3k (Sharpe -0.29)
- breakback: EU day +$77k vs US evening +$5k
- setpoint/setpoint-serve: EU day positive, US evening negative

---

## Track 2: Taker Flow Toxicity (RQ4) — NOT TRADEABLE

**Critical data defect**: all 2.88M trades in DB are bearish on YES player.
- `yes/bid` (sell YES): 71% of trades
- `no/ask` (buy NO): 29%
- `yes/ask` (buy YES): **0 trades**
- `no/bid` (sell NO): **0 trades**

Signed imbalance is constant -1. VPIN bull-bear split not computable.
Likely scraper/ingest gap — aggressive buys on ask side never recorded.

Volume-pressure proxy (rolling bearish volume): median correlation +0.02
with forward 60s mid move. Split 55/45 positive/negative. No edge.

Strategy (short YES on high volume): n=111k, hit 22.4%, net -$3025,
Sharpe -4.96. Loses on every series.

**Action**: fix ingest pipeline to capture `yes/ask` and `no/bid` taker trades.

---

## Track 3: Orderbook Depth Dynamics (RQ9) — NOT TRADEABLE

Top-of-book depth imbalance predicts direction but too weak to trade.

- Q1 (deep bid): avg fwd 60s move +0.00469
- Q5 (deep ask): avg fwd 60s move -0.00433
- Per-market corr mean -0.133, 69% markets p<0.05

Predicted move ~0.5c over 60s. Spread + 1c fee = minimum 1c cost.
Signal too weak to overcome fees.

**Not a volatility predictor**: |imbalance|>0.5 has *smaller* forward |move|
than |imbalance|<0.2. Deep one-sided books are calmer, not more volatile.

Strategy (buy YES on deep bid): n=88k, net -$4321, Sharpe -8.5.

Could have value as feature in multi-factor model at lower fee / longer horizon.

---

## Track 4: Volume/OI Predictor (RQ14) — NOT TRADEABLE

Volume ratio does NOT predict outcome. Price dominates.

- Brier score: volume ratio 0.36 vs price 0.23 (price better by 0.13)
- Disagreement cases (79): volume wins 16.5%, price wins 83.5%
- Strategy (volume+price combo): n=129, Sharpe -0.47. Worse than price baseline.

**Counterintuitive**: high-volume markets have LOWER favorite win rates
(Q1 75% vs Q4 63%). Volume tracks attention, not informed money.

---

## Track 5: Sharpen Winners

### Best filter per strategy (from live DB orders)

| Strategy | Filter | n before->after | Sharpe before->after | P&L before->after |
|---|---|---|---|---|
| cross-arb-favorite | series=KXITFMATCH | 188->47 | 0.63->1.18 | $1750->$676 |
| setpoint-cheap | size top quartile | 155->30 | 0.30->0.67 | $985->$832 |
| cross-arb | size 10-12.5 band | 2014->93 | 0.23->0.62 | $4441->$300 |
| convexpool | 30-60min before close | 9011->2249 | 0.12->0.36 | $4724->$3154 |
| setdown | positive series only | 67935->34028 | 0.03->0.24 | $6920->$36209 |
| setpoint | series=KXITFWMATCH | 379->43 | 0.13->0.51 | $1034->$524 |
| setpoint-serve | size bottom quartile | 235->47 | 0.13->1.90 | $739->$31 |
| adout | series=KXWTAMATCH | 143->63 | 0.14->0.25 | $133->$105 |

**Series is the dominant edge driver.** setdown's entire edge comes from 4 series;
the other 4 lose $34k. Same for setpoint/setpoint-serve.

---

## Implemented Strategies

7 filtered variants added to `internal/algorithms/` + `internal/backtest/factories.go`
+ `internal/strategies/builder.go`:

| Name | Base | Filter | Config Fields Added |
|---|---|---|---|
| setdown-series | setdown | ATP Challenger + ATP + WTA + ITF doubles | SeriesFilter |
| setdown-noon | setdown | UTC 11-13 | UTCHourStart/End |
| tiebreak-itfwdoubles | tiebreak | KXITFWDOUBLES only | SeriesFilter |
| tiebreak-eu-daytime | tiebreak | UTC 10-16 | UTCHourStart/End |
| cross-arb-favorite-itf | cross-arb-favorite | KXITFMATCH only | SeriesFilter |
| setpoint-set1 | setpoint | Set 1 only | MaxSetNumber |
| convexpool-wta | convexpool | KXWTAMATCH only | SeriesFilter |

Shared filter helpers in `internal/algorithms/filters.go`:
- `seriesMatches(series, filter)` — empty filter = match all
- `utcHourMatches(ts, start, end)` — wraparound support (e.g. 18-4)

Each strategy gained: `SeriesFilter []string`, `UTCHourStart/End int` config
fields, `series map` field, `SetSeriesTicker` (SeriesSetter), `WithDB`
constructor for live mode.

---

## Backtest Results

```bash
go run ./cmd/backtest -strategy <name>
```

### Filtered vs Base (backtest engine, 5626 matches)

| Strategy | n | Hit | Net P&L | ROI | Sharpe | PF |
|---|---|---|---|---|---|---|
| setdown (base) | 301 | 45.2% | +$89 | 5.9% | 0.051 | 1.11 |
| setdown-series | 147 | 45.6% | +$43 | 5.9% | 0.051 | 1.11 |
| setdown-noon | 25 | 40.0% | -$3 | -2.7% | -0.022 | 0.96 |
| tiebreak (base) | 370 | 38.1% | -$122 | -6.8% | -0.056 | 0.89 |
| tiebreak-itfwdoubles | 9 | 55.6% | +$18 | 40.1% | 0.310 | 1.90 |
| tiebreak-eu-daytime | 95 | 36.8% | -$56 | -12.3% | -0.104 | 0.81 |
| cross-arb-favorite (base) | 664 | 22.6% | +$766 | 23.2% | 0.068 | 1.30 |
| cross-arb-favorite-itf | 163 | 20.2% | +$164 | 20.4% | 0.048 | 1.26 |
| setpoint (base) | 161 | 59.0% | -$80 | -9.9% | -0.112 | 0.76 |
| setpoint-set1 | 118 | 59.3% | -$84 | -14.2% | -0.174 | 0.65 |
| convexpool (base) | 28898 | 38.8% | -$6487 | -4.6% | -0.028 | 0.92 |
| **convexpool-wta** | 6173 | 47.2% | **+$1071** | 3.6% | **0.025** | **1.07** |

### Winner: convexpool-wta

Base convexpool: -$6487, Sharpe -0.028, PF 0.92 (net negative).
WTA-only: +$1071, Sharpe 0.025, PF 1.07 (net positive).

**Flipped from losing to winning.** WTA markets are where convexpool's
Markov-vs-market edge actually exists. Other series (especially ITF women's)
destroy the edge — the Markov model's serve-win assumption doesn't hold
for ITF women's (52% hold vs model's 64% default).

### Runner-up: tiebreak-itfwdoubles

Sharpe 0.310 vs base -0.056. Positive but n=9 — too small to trust.
Directionally confirms the research finding (KXITFWDOUBLES Sharpe 2.07
from live orders). Needs more data.

---

## Research-to-Backtest Gap

Live DB orders show much higher order counts than backtest (e.g. setdown:
67k live vs 301 backtest). Reasons:

1. **Live system fires on every price tick** that meets criteria, while
   backtest replays a subset of ticks (only `msg_type='ticker'` with
   valid bid/ask). Live may fire on trade ticks too.
2. **`fired` map dedup**: both paths check `fired[eventTicker]`, but live
   system may register/unregister markets differently, resetting state.
3. **Score-based path (OnPoint)**: only fires for matches with point data
   (92k points across ~470 matches). Many matches have ticks but no points.

**Backtest is more conservative.** Filters that looked strong on live DB
orders (setdown-series 9x Sharpe) don't improve in backtest because the
backtest already generates fewer, higher-quality signals.

The one filter that survives: **convexpool-wta**. The series filter
eliminates markets where the Markov model's serve assumption is wrong
(ITF women's 52% hold vs 64% default), which is a structural edge, not
a statistical artifact.

---

## What Didn't Work

1. **Taker flow toxicity (RQ4)**: data defect — no bullish trades recorded.
   Fix ingest first.
2. **Depth dynamics (RQ9)**: real signal (~0.5c over 60s) but too weak
   vs 1c fee. Not tradeable standalone.
3. **Volume predictor (RQ14)**: price dominates. Volume ratio adds nothing.
4. **setdown-noon**: UTC hour filter too narrow (n=25). Noon UTC edge
   doesn't survive in backtest.
5. **tiebreak-eu-daytime**: EU daytime filter makes tiebreak worse in
   backtest, not better. Research finding was on live orders.
6. **setpoint-set1**: set 1 filter makes setpoint worse. The set 2+
   losses in live data don't appear in backtest.
7. **cross-arb-favorite-itf**: ITF filter slightly worse than base.
   Base already captures the edge across all series.

---

## Next Steps

1. **Ship convexpool-wta** — only variant that flips negative to positive
   in backtest. Already registered in live builder.
2. **Monitor tiebreak-itfwdoubles** — positive direction, needs n>30.
3. **Fix taker flow ingest** — capture `yes/ask` and `no/bid` trades.
   Without bullish flow, VPIN toxicity is untestable.
4. **Per-player serve priors** — Markov model uses global pServe=0.64.
   ITF women's hold 52%, ATP 61%. Per-series pServe would improve
   convexpool across all series, not just WTA.
5. **convexpool time-to-close filter** — research found 30-60min before
   close is sweet spot (Sharpe 0.36). Needs close_ts tracking in
   ConvexPoolStrategy (CloseTimeStrategy interface). Not implemented yet.
6. **Multi-factor model** — depth imbalance + flow + price velocity as
   features. Individual signals too weak, but combined may clear fees.
