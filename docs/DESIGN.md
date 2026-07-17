# Kalshi Tennis ML — System Design Document

> A ghost trading system that predicts tennis match outcomes using Kalshi market data + API-Tennis real-time scores, and executes trades when the model detects a pricing gap. Built on research from ~20 papers on tennis win probability, betting market efficiency, and sports prediction ML.

---

## Table of Contents

1. [System Architecture](#1-system-architecture)
2. [Data Pipeline](#2-data-pipeline)
3. [Feature Engineering](#3-feature-engineering)
4. [Model Zoo](#4-model-zoo)
5. [Training Pipeline](#5-training-pipeline)
6. [Calibration & Evaluation](#6-calibration--evaluation)
7. [Ghost Trading Engine](#7-ghost-trading-engine)
8. [Deployment](#8-deployment)
9. [Research References](#9-research-references)
10. [Appendix: Benchmark Expectations](#10-appendix-benchmark-expectations)

---

## 1. System Architecture

```
                          ┌──────────────────────────────────────┐
                          │        Cloud VM (24/7 runner)         │
                          │                                       │
  Kalshi ──WS/Signal──▶  │  ┌──────────┐    ┌───────────────┐   │
    WebSocket             │  │ Go Ghost │    │ Python ML     │   │
                          │  │ Trader   │◄──►│ Inference     │   │
  API-Tennis ──WS──▶     │  │ (ticks)  │    │ Engine        │   │
    Real-Time              │  │          │    │ (predictions) │   │
                          │  └─────┬────┘    └───────┬───────┘   │
                          │        │                  │           │
                          │  ┌─────▼──────────────────▼───────┐   │
                          │  │      SQLite DB                  │   │
                          │  │  events / markets / ticks       │   │
                          │  │  orders / predictions / trades  │   │
                          │  └─────▲───────────────────────────┘   │
                          │        │                               │
                          │  ┌─────┴────────────┐                  │
                          │  │ Offline Trainer   │                  │
                          │  │ (cron: daily)     │                  │
                          │  └──────────────────┘                  │
                          └──────────────────────────────────────┘
```

**Three processes on the VM:**

| Process | Language | Role |
|---|---|---|
| `ghost-trader` | Go | WebSocket feeds, REST scans, tick storage, API-Tennis scraper (existing) |
| `ml-engine` | Python | Feature engineering → model inference → trade decisions (new) |

**One offline cron job:**
| Process | Frequency | Role |
|---|---|---|
| `trainer` | Daily | Reload data, train all model permutations, evaluate, deploy best |

### Architectural Rationale

From the literature — **the fused model (statistical + market) beats either alone** (Montrucchio, Barbierato & Gatti 2026; Teles 2026). The architecture supports both score-only and fused variants, with the ghost trade engine deciding which to deploy based on validation performance.

---

## 2. Data Pipeline

### 2.1 Existing Data (from ghost-trader)

Already being collected:
```
events       — match metadata (37K+ and growing)
markets      — 2 per event, with result (yes/no, 1.0/0.0)  
ticks        — price, bid/ask, volume, OI, trades (378K+ and growing)
orderbook_events — full order book snapshots + deltas (9.8M+)
```

Key tick schema fields used by the ML pipeline:
```
ts              INTEGER    — server unix ms
market_ticker   TEXT       — KXATPMATCH-...-COL format
price           REAL       — 0.00 to 1.00
yes_bid/ask     REAL       — bid-ask spread
volume          REAL       — cumulative contracts
open_interest   REAL       — outstanding contracts
```

### 2.2 API-Tennis Real-Time Data (built — `internal/apitennis`)

**Why API-Tennis**: WebSocket push model — no polling delay. Covers ATP/WTA/ITF/Challenger. Provides real-time match updates on every state change (point won, game/set completed).

**Implementation**: Go package `internal/apitennis` connects to `wss://wss.api-tennis.com/live?APIkey=<key>&timezone=<tz>`. Auto-reconnects with exponential backoff. Disabled by default; enable via `apitennis_enabled: true` in config.yaml.

**Tables**: No dedicated tables — API-Tennis data drives strategy signals directly via WebSocket push. Market registration and player name matching cached in-memory.

### 2.3 Pre-Match Player Data (needs building)

The Markov chain needs per-player serve-win probabilities. Sources:
- **ATP/WTA official stats**: historical serve/return win rates per player per surface
- **Elo ratings**: from tennis-data repositories (e.g., Jeff Sackman / GitHub tennis Elo)
- **Rankings**: live ATP/WTA rankings (freely available)

Store in a `player_stats` table:
```
player_name        TEXT PRIMARY KEY
elo_rating         REAL
serve_win_pct_hard REAL     — career serve win % on hard courts
serve_win_pct_clay REAL
serve_win_pct_grass REAL
return_win_pct_hard REAL
return_win_pct_clay REAL
return_win_pct_grass REAL
last_updated       INTEGER
```

---

## 3. Feature Engineering

### 3.1 Score-State Features (from points table)

**Markov Chain Baseline** (Klaassen & Magnus 1998–2014, verified by Wang & Drekic 2026):
The Markov chain computes closed-form win probability from the score state using serve/return win probabilities. The recursive formula:

```
P_win_game(a, b, serving, p, q):
  # a = server's points, b = receiver's points
  # p = P(server wins a point on serve)
  # q = P(returner wins a point on return)
  # Returns P(server wins the game)
  Uses standard DTMC with absorbing states at game won/lost.

P_win_set(A, B, a, b, P_game):
  # A, B = games won in set
  # a, b = points in current game
  # P_game = prob of winning current game from its state
  # Uses tiebreak formula at 6-6
  # Returns P(winning the set)
  
P_win_match(S, A, B, P_set):
  # S = sets won so far
  # A, B = games in current set
  # P_set = prob of winning current set
  # Returns P(winning the match)
```

Features derived from the Markov chain:
```
markov_win_prob         REAL    — P(win match) from current score
markov_game_prob        REAL    — P(win current game)
markov_set_prob         REAL    — P(win current set)
markov_point_importance REAL    — change in win prob from winning vs losing this point
                               (Klaassen & Magnus's measure: higher = more decisive)
```

**Score State Indicators** (O'Donoghue 2012 — key finding: double break in deciding set = 95-97% certainty):
```
sets_won                INT     — 0 or 1 (best of 3)
games_won_set           INT     — games won in current set
games_lost_set          INT     — games lost in current set
game_score              TEXT    — "0-0" through "AD-40"
is_break_point          BOOL    — receiving at 30-40, 15-40, 0-40, or ad-out
is_set_point            BOOL    — serving or receiving at set-winning state
is_match_point          BOOL    — serving or receiving at match-winning state
is_tiebreak             BOOL    — current game is a tiebreak
serving                 BOOL    — 1 if this player is serving
serving_for_match       BOOL    — serving at match-winning game score
serving_for_set         BOOL    — serving at set-winning game score
up_break                BOOL    — has a break in this set
double_break_up         BOOL    — has two breaks in this set (true point of no return)
down_break              BOOL    — trailing by a break
double_break_down       BOOL    — trailing by two breaks
score_state_id          INT     — compact encoding of (set, game, point) state
```

**Within-Match Dynamics** (Lei et al. 2024 — HMM + XGBoost for momentum detection):
```
last_5_points_won       INT     — out of last 5 points
last_10_points_won      INT     — out of last 10 points
points_streak           INT     — consecutive points won (positive if this player)
serves_held_consecutive INT     — consecutive service games held
return_games_won        INT     — return games won this match
avg_point_length_sec    REAL    — average rally duration (from API-Tennis if available)
```

### 3.2 Market Features (from ticks table)

**Current Market State:**
```
mid_price               REAL    — (yes_bid + yes_ask) / 2
price                   REAL    — last traded price
spread                  REAL    — yes_ask - yes_bid
log_spread              REAL    — ln(ask / bid)
bid_depth               REAL    — yes_bid_size (contracts)
ask_depth               REAL    — yes_ask_size
book_imbalance          REAL    — (bid_depth - ask_depth) / (bid_depth + ask_depth)
volume                  REAL    — cumulative contracts traded
open_interest           REAL    — outstanding contracts
```

**Price Dynamics (rolling windows of N ticks):**
```
price_ma_10             REAL    — moving average, 10 ticks
price_ma_100            REAL    — moving average, 100 ticks
price_std_10            REAL    — rolling volatility, 10 ticks
price_std_100           REAL    — rolling volatility, 100 ticks
price_velocity          REAL    — (ma_10 - ma_100) / ma_100
price_acceleration      REAL    — change in velocity since last tick
price_min_50            REAL    — min price, last 50 ticks
price_max_50            REAL    — max price, last 50 ticks
volume_delta            REAL    — volume since last tick
oi_delta                REAL    — OI change since last tick
```

**Market vs Model Gap (for residual/fused models):**
```
market_minus_markov     REAL    — market_price - markov_win_prob
                             This IS the edge signal for the residual model.
                             Positive = market overprices this player.
                             Negative = market underprices this player.
```

### 3.3 Pre-Match Features (external lookup)

These are computed once per match and don't change during play:
```
player_elo              REAL    — Elo rating (from tennis data repository)
opponent_elo            REAL    — opponent's Elo
elo_diff                REAL    — player_elo - opponent_elo
serve_win_career        REAL    — historical serve win % (surface-specific)
return_win_career       REAL    — historical return win % (surface-specific)
surface_clay            BOOL    — one-hot encoded
surface_grass           BOOL
surface_hard            BOOL
tier_slam               BOOL    — one-hot: grand slam
tier_masters            BOOL    — ATP Masters 1000
tier_250_500            BOOL    — ATP 250/500
tier_challenger         BOOL    — Challenger
tier_itf                BOOL    — ITF
tier_wta                BOOL    — WTA (separate from ATP)
is_bo5                  BOOL    — best of 5 sets (slams only)
match_round             TEXT    — qualifying / R64 / R32 / R16 / QF / SF / F
```

### 3.4 Feature Set Permutations

We train on these feature sets to compare:

| Set | Features Included | Rationale |
|---|---|---|
| **S1: Score only** | All score-state + pre-match features. No market features. | Pure statistical model. Compared against market to find gaps. |
| **S2: Market only** | Current market + price dynamics features. No score data. | Baseline: what does the market microstructure tell us? |
| **S3: Fused** | Score + market + pre-match features. Everything. | Highest accuracy. Risk: model mimics market. |
| **S4: Residual** | `market_minus_markov` as target. Score + market features as inputs. | Directly predicts the edge. Learns market biases. |
| **S5: Markov + Market** | Markov chain prob + market features. No raw score state. | Lighter than full fused, still captures market info. |

---

## 4. Model Zoo

Every permutation of (model_class × feature_set × calibration) is trained and compared.

### 4.1 Tier 1 — Baselines (must beat to have edge)

| # | Model | Feature Set | Why It's Here | Citation |
|---|---|---|---|---|
| 1 | **Constant: pre-match market price** | — | Floor. If you can't beat the opening line consistently, you have no edge. | Wilkens 2021 |
| 2 | **Logistic Regression** | S1 (score) | Simplest calibrated model. Inherently well-calibrated (log-loss optimal). Fast to train. | Klaassen & Magnus baseline |
| 3 | **Markov Chain (closed-form)** | S1 (score only) | The structural truth. No training — just serve/return rates. On par with ML at ~70% accuracy. | Wang & Drekic 2026 |

### 4.2 Tier 2 — Gradient Boosted Trees

| # | Model | Feature Set | Why | Citation |
|---|---|---|---|---|
| 4 | **LightGBM** | S1 (score) | Fast, handles categoricals, shallow trees calibrate naturally. Production gold standard (nflfastR). | nflfastR production system |
| 5 | **LightGBM** | S3 (fused) | Highest raw accuracy. Must verify it's not mimicking via SHAP. | — |
| 6 | **LightGBM** | S4 (residual) | Directly predicts the gap. Learns systematic market biases. | — |
| 7 | **LightGBM** | S5 (markov + market) | Light fusion: uses Markov prob as input, avoids raw score complexity. | — |
| 8 | **XGBoost** | S1 (score) | Slightly more accurate than LightGBM, more HPs to tune. Used in Lei et al. 2024. | Lei et al. 2024 |
| 9 | **XGBoost** | S3 (fused) | Top contender for raw accuracy. | — |
| 10 | **XGBoost** | S4 (residual) | Residual variant. | — |
| 11 | **CatBoost** | S1 (score) | Best categorical handling. Supports online/incremental learning (weekly retrain). | — |
| 12 | **CatBoost** | S3 (fused) | Fused variant with online update. | — |
| 13 | **Random Forest** | S1 (score) | Simple, SHAP-interpretable. Used in SOTA H-NHMC paper. | Quan, Chen & Chen 2026 |
| 14 | **Random Forest** | S3 (fused) | Fused variant. | — |

### 4.3 Tier 3 — Sequence Models (later phase, data-heavy)

| # | Model | Feature Set | When to Use | Citation |
|---|---|---|---|---|
| 15 | **LSTM** | S1 over point sequences | 500+ matches. Captures within-match temporal patterns. Small benefit vs GBM. | Lei et al. 2024 |
| 16 | **GRU** | S1 over point sequences | Simpler than LSTM, similar performance. | — |
| — | **Transformer** | — | **Skip.** Literature finds no benefit over GBM for this problem. The state space (~400 states) is too small. | Yan et al. 2024 |

### 4.4 Tier 4 — Ensembles

| # | Model | Why | Citation |
|---|---|---|---|
| 17 | **Convex Pool: w×Markov + (1-w)×Market** | Learn single weight w on validation data. Simple, interpretable, hard to overfit. | Teles 2026 |
| 18 | **Stacked Ensemble: top 3 models** | Outputs of best 3 models → Logistic Regression meta-model. Production standards. | — |
| 19 | **Average Ensemble: top 5 models** | Simple average of best 5 uncorrelated models. Reduces variance. | — |

### 4.5 Calibration Methods (applied after every model)

| Method | When to Use | Citation |
|---|---|---|
| **Platt scaling** | Default for all models. Parametric, good with limited data. | Walsh & Joshi 2024 |
| **Isotonic regression** | Only with 500+ validation samples. Non-parametric, more flexible, overfits on small data. | Walsh & Joshi 2024 |
| **Temperature scaling** | Quick fix for NN/LSTM outputs. Single parameter. | — |
| **None** | Baseline: compare raw model output vs calibrated. | — |

### 4.6 Total Permutation Count

```
Model classes:    14 (tier 2 + tier 3) 
Feature sets:     5 (S1-S5)
Calibration:      4 (none, platt, isotonic, temperature)
Sequence models:  2 (LSTM, GRU) × 1 feature set (S1)
────────────────────────────────────────────
                 ≈ 14 × 5 × 4 + 2 = ~282 model variants
                 + 3 ensembles
                 + 3 baselines
                 = ~288 total
```

All variants logged to a `model_runs` table with metrics. Only the best 3-5 are deployed.

---

## 5. Training Pipeline

### 5.1 Data Assembly

A training example is a single row at a point in time within a match:

```sql
-- One training example per tick (when a point was scored nearby)
SELECT 
    -- Label (from markets)
    CASE m.result WHEN 'yes' THEN 1 ELSE 0 END AS label,
    
    -- Pre-match features
    ps.elo_rating, ps.serve_win_pct_hard, e.competition,
    
    -- Score-state features (from points)
    p.set_number, p.game_number, p.point_score,
    p.game_score, p.serving, p.is_break_point,
    
    -- Market features (from ticks)
    t.ts, t.price, t.yes_bid, t.yes_ask, 
    t.yes_bid_size, t.yes_ask_size, t.volume, t.open_interest,
    
    -- Metadata for validation splitting
    e.event_ticker  -- used for group k-fold
    
FROM ticks t
JOIN markets m ON m.market_ticker = t.market_ticker
JOIN events e ON e.event_ticker = m.event_ticker
JOIN points p ON p.match_ticker = e.event_ticker
    AND ABS(p.ts_ms - t.ts) < 2000
LEFT JOIN player_stats ps ON ps.player_name = m.player_name

WHERE m.result IS NOT NULL AND m.result != ''
ORDER BY e.event_ticker, t.ts
```

### 5.2 Cross-Validation Strategy

**Crucially: match-level grouped k-fold, NEVER random shuffle.**

The literature is unanimous: shuffle leaks future information from the same match into training (Wilkens 2021; Walsh & Joshi 2024).

```python
from sklearn.model_selection import GroupKFold

groups = df['event_ticker']  # each match is a group
gkf = GroupKFold(n_splits=5)

for train_idx, val_idx in gkf.split(X, y, groups):
    X_train, X_val = X.iloc[train_idx], X.iloc[val_idx]
    y_train, y_val = y.iloc[train_idx], y.iloc[val_idx]
    # Train on matches from 3/1/2026
    # Validate on matches from 3/8/2026
```

**Temporal holdout**: Additionally, hold out the most recent 2 weeks of data as a final test set. Never inspect it until all model selection is done.

### 5.3 Training Script Structure

```
train.py
  │
  ├── load_data()           # SQL → pandas DataFrame
  ├── engineer_features()   # markov probs, rolling windows, etc.
  ├── split_data()          # temporal + group k-fold
  │
  ├── run_model_zoo():
  │   for feature_set in ['S1','S2','S3','S4','S5']:
  │     for model_class in [LogisticRegression, LGBM, XGB, CatBoost, RF]:
  │       for calibration in [None, Platt, Isotonic, Temperature]:
  │         └── train + evaluate → log to model_runs table
  │
  ├── train_ensembles()      # best-of-N convex pool, stacked
  ├── select_best_models()   # rank by ECE + log-loss + Sharpe on val
  ├── calibrate_holdout()    # Platt on holdout set
  └── deploy()               # save top-3 to models/ directory
```

### 5.4 Model Run Logging

```sql
CREATE TABLE model_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    run_ts          INTEGER NOT NULL,          -- unix ms
    model_name      TEXT NOT NULL,              -- e.g. "lgbm_s3_platt"
    model_class     TEXT NOT NULL,              -- "lightgbm", "xgboost", etc.
    feature_set     TEXT NOT NULL,              -- "S1", "S2", "S3", "S4", "S5"
    calibration     TEXT,                        -- "none", "platt", "isotonic", "temperature"
    hyperparams     TEXT,                        -- JSON of params used
    
    -- Metrics on validation set
    val_log_loss        REAL,
    val_brier           REAL,
    val_ece             REAL,                   -- expected calibration error
    val_auc             REAL,
    val_accuracy        REAL,
    val_sharpe          REAL,                   -- simulated ghost trade sharpe
    val_num_matches     INTEGER,                -- number of matches in val set
    val_num_examples    INTEGER,                -- number of rows in val set
    
    -- Metrics on test holdout
    test_log_loss       REAL,
    test_brier          REAL,
    test_ece            REAL,
    test_sharpe         REAL,
    
    -- Feature importance
    top_features        TEXT,                    -- JSON: {feature: importance, ...}
    market_correlation  REAL,                    -- corr(p_pred, p_market) — detect mimicking
    
    model_path          TEXT,                    -- path to saved model file
    deployed            INTEGER DEFAULT 0        -- 1 if currently deployed
);
CREATE INDEX idx_model_runs_metrics ON model_runs(val_ece, val_sharpe);
```

---

## 6. Calibration & Evaluation

### 6.1 Why Calibration Trumps Accuracy

**The single most important finding from the literature** (Walsh & Joshi 2024, cited 23 times):

> A calibration-optimized model outperformed accuracy-optimized models by **69.86% in average returns** across NBA seasons 2014-2019.

This is because betting profit depends on correct probability estimates (for expected value), not correct classifications. A model that's right 72% of the time but poorly calibrated can lose money. A model that's right 68% of the time but perfectly calibrated can be profitable.

**Your primary metric is Expected Calibration Error (ECE), not accuracy.**

| Metric | Priority | Why |
|---|---|---|
| ECE | **1st** | Measures if predictions match reality. ECE < 0.02 is the target. |
| Log-loss | **2nd** | Proper scoring rule for probabilities. Lower is better. |
| Brier score | **3rd** | Mean squared error of probabilities. Lower is better. |
| Sharpe (ghost) | **4th** | Simulated P&L from trading on model's gaps. > 1.0 is good. |
| AUC-ROC | **5th** | Separability. > 0.80 is fine. |
| Accuracy | **Last** | ~70-75% is the literature ceiling. Don't optimize for this. |

### 6.2 Reliability Diagrams

Every model gets a reliability diagram (predicted probability vs actual frequency):

```
   1.0 ┤          ╱
       │        ╱╱      ★= well-calibrated (on diagonal)
       │      ╱        ○ = under-confident (above diagonal)
       │    ╱          △ = over-confident (below diagonal)
       │  ╱
       │╱
   0.0 ┼────┬────┬────┬────
      0.0  0.2  0.4  0.6  0.8  1.0
           Predicted probability
```

Models with significant deviation from the diagonal are discarded regardless of other metrics.

### 6.3 The Mimicking Problem — Detection & Mitigation

When market price is used as a feature, the model may learn to copy the market rather than find genuine edges.

**Detection** (Montrucchio et al. 2026):
```python
corr = df['predicted_prob'].corr(df['market_price'])
if corr > 0.95:
    warning = "Model is mimicking market — check SHAP importance"
    
# SHAP analysis
import shap
explainer = shap.TreeExplainer(model)
shap_values = explainer.shap_values(X_val)
shap.summary_plot(shap_values, X_val)
# If market_price dominates shap values (>50% importance) → mimicking
```

**Mitigation:**
1. **Ablation**: always train S1 (score-only) alongside fused variants. If S1 performs similarly to S3, use S1 (cleaner signal).
2. **Residual approach**: train S4 to directly predict the gap, not the outcome. This forces the model to learn market biases.
3. **Convex pool**: `p_final = w × p_score_model + (1-w) × p_market`. Learn w on validation. w < 0.5 means the score model adds independent information.
4. **Feature restriction**: use markov_win_prob as the only "market-proximate" feature, not raw price.

### 6.4 Permutation Feature Importance

For every model, compute:
```python
baseline = log_loss(y_val, model.predict_proba(X_val))
importance = {}
for col in X_val.columns:
    X_perm = X_val.copy()
    X_perm[col] = np.random.permutation(X_perm[col])
    perm_loss = log_loss(y_val, model.predict_proba(X_perm))
    importance[col] = perm_loss - baseline  # higher = more important
```

This shows which score states and market features the model actually relies on.

---

## 7. Ghost Trading Engine

### 7.1 Strategy Definition

**Core logic**: Trade when `model_prob - market_price > threshold` (undervalued) or `market_price - model_prob > threshold` (overvalued).

**Threshold is learned on validation data, not guessed:**
```python
thresholds = np.arange(0.02, 0.30, 0.01)
best_sharpe = -np.inf
best_threshold = None

for t in thresholds:
    trades = backtest(predictions, market_prices, threshold=t, labels=y_val)
    sharpe = compute_sharpe(trades)
    if sharpe > best_sharpe:
        best_sharpe = sharpe
        best_threshold = t
```

### 7.2 Trade Rules

```
For each tick in an active match:

1. Compute features → model predicts P(win)
2. gap = P(win) - market_price

3. ENTRY CONDITIONS (all must be met):
   - abs(gap) > entry_threshold          (default: learned from validation)
   - spread < max_spread                 (default: 0.05, 5 cents)
   - time_until_close > 60s              (don't enter with seconds left)
   - NOT already in position for this player
   - model confidence interval does NOT include market price (statistically significant gap)

4. EXIT CONDITIONS (any one triggers):
   - market_price >= 0.95                (profit taken)
   - market_price <= 0.05                (loss cut for winner position)
   - abs(gap) < exit_threshold           (gap closed)
   - match ended (settlement event received)
   - max_hold_time exceeded              (default: 60 min, prevent overnight)

5. POSITION SIZING:
   - flat_base = 10% of available capital
   - scaled = flat_base × abs(gap) / entry_threshold
   - capped = min(scaled, max_single_bet)  (default: 25% of capital)
   - Trade BOTH players: buy winner at low price, sell loser at high price
     (long/short pair trade neutralizes contract-level risk)
```

### 7.3 Backtesting

Before live trading, backtest on historical data:

```python
def backtest(df, model, threshold, spread_limit=0.05):
    """
    df: one row per tick per match, with model_prob and label
    Simulates trading on historical data and returns P&L
    """
    trades = []
    for event_ticker, match_df in df.groupby('event_ticker'):
        position = None
        for _, row in match_df.iterrows():
            gap = row['model_prob'] - row['market_price']
            
            # Entry
            if position is None and abs(gap) > threshold and row['spread'] < spread_limit:
                position = {
                    'entry_time': row['ts'],
                    'entry_price': row['market_price'],
                    'side': 'buy' if gap > 0 else 'sell',
                    'size': compute_position_size(gap)
                }
            
            # Exit
            if position is not None:
                exit_signal = False
                if row['market_price'] >= 0.95:
                    exit_signal = True
                elif abs(row['model_prob'] - row['market_price']) < exit_threshold:
                    exit_signal = True
                elif row['result'] is not None:
                    exit_signal = True  # match settled
                
                if exit_signal:
                    pnl = position['size'] * (
                        (row['market_price'] - position['entry_price']) 
                        if position['side'] == 'buy' 
                        else (position['entry_price'] - row['market_price'])
                    )
                    trades.append({**position, 'exit_price': row['market_price'], 'pnl': pnl})
                    position = None
    
    return trades
```

### 7.4 Monitoring Dashboard (what to log per trade)

```sql
CREATE TABLE trades (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,          -- entry time
    event_ticker    TEXT NOT NULL,
    market_ticker   TEXT NOT NULL,
    
    -- Entry
    entry_price     REAL NOT NULL,
    model_prob      REAL NOT NULL,
    gap             REAL NOT NULL,             -- model - market
    side            TEXT NOT NULL,             -- "buy" (undervalued) or "sell" (overvalued)
    size            REAL NOT NULL,             -- contracts
    
    -- Exit
    exit_price      REAL,
    exit_time       INTEGER,
    exit_reason     TEXT,                      -- "price_target", "gap_closed", "settled", "timeout"
    pnl             REAL,                      -- gross P&L in dollars
    pnl_pct         REAL,                      -- return on capital
    
    -- Model used (which deployed model made this trade)
    model_run_id    INTEGER,
    FOREIGN KEY (model_run_id) REFERENCES model_runs(id)
);

-- Daily P&L summary
CREATE VIEW daily_pnl AS
SELECT 
    datetime(ts/1000, 'unixepoch', 'start of day') AS day,
    COUNT(*) AS trades,
    SUM(CASE WHEN pnl > 0 THEN 1 ELSE 0 END) AS winning_trades,
    ROUND(AVG(pnl), 4) AS avg_pnl,
    ROUND(SUM(pnl), 4) AS total_pnl,
    ROUND(SUM(pnl) / (SELECT AVG(size*entry_price) FROM trades), 4) AS daily_return
FROM trades
WHERE exit_price IS NOT NULL
GROUP BY day
ORDER BY day;
```

---

## 8. Deployment

### 8.1 Cloud VM (Recommended Specs)

| Resource | Minimum | Notes |
|---|---|---|
| CPU | 2 cores | ML inference is lightweight. Training needs more. |
| RAM | 4 GB | SQLite + feature vectors |
| Storage | 50 GB | DB grows ~1.5 GB/week. Plan for 4+ months. |
| OS | Ubuntu 22.04 | Standard |
| Cost | ~$25-40/month | Cheapest DigitalOcean/Linode/Vultr droplet |

### 8.2 Stack

| Component | Technology | Rationale |
|---|---|---|
| Database | SQLite (WAL mode) | Already in use. Single-writer pattern works for ghost-trader. |
| WebSocket client | Go (existing ghost-trader) | Already works. No need to rewrite. |
| ML inference | Python 3.11 + ONNX runtime | ONNX for fast inference without Python ML deps. |
| Model training | Python 3.11 | LightGBM, XGBoost, sklearn, shap, optuna |
| Scheduler | systemd timers or cron | Run trainer daily at 4 AM. |
| Monitoring | Python script + email/Slack | Daily P&L report. Alert on error rates. |
| Code sync | git pull + restart | Simple, reliable for solo dev. |

### 8.3 File Layout

```
/opt/kalshi-ml/
├── ghost-trader/           # Go binary + config (existing, includes API-Tennis scraper)
├── ml-engine/              # Python ML (new)
│   ├── inference.py        # Live inference loop
│   ├── features.py         # Feature engineering
│   ├── markov.py           # Markov chain closed-form
│   ├── models/             # Saved model files
│   │   ├── lgbm_s1_platt.onnx
│   │   ├── xgb_s3_platt.onnx
│   │   └── catboost_s4_isotonic.onnx
│   └── requirements.txt
├── trainer/                # Python training (new)
│   ├── train.py            # Main training pipeline
│   ├── model_zoo.py        # All model definitions
│   ├── calibrate.py        # Platt/isotonic implementation
│   ├── backtest.py         # Historical sim
│   └── requirements.txt
├── data/
│   └── kalshi_tennis.db    # SQLite database (symlink or mounted)
├── config.yaml             # Shared config
└── deploy.sh               # Pull + rebuild + restart
```

### 8.4 Systemd Services

```ini
# /etc/systemd/system/kalshi-ml.service
[Unit]
Description=Kalshi ML Engine
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/kalshi-ml
ExecStart=/usr/bin/python3 /opt/kalshi-ml/ml-engine/inference.py
Restart=always
RestartSec=10
User=ubuntu

[Install]
WantedBy=multi-user.target
```

### 8.5 Cron Schedule

```cron
# Daily: retrain models at 4 AM
0 4 * * * cd /opt/kalshi-ml && python3 trainer/train.py >> /var/log/kalshi-train.log 2>&1

# Every 30 min: health check + match scan
*/30 * * * * cd /opt/kalshi-ml && python3 ml-engine/health_check.py >> /var/log/kalshi-health.log 2>&1

# Daily: P&L report at 9 AM
0 9 * * * cd /opt/kalshi-ml && python3 ml-engine/report.py >> /var/log/kalshi-report.log 2>&1
```

---

## 9. Research References

### Core Tennis Win Probability

| Citation | Key Finding |
|---|---|
| **Klaassen & Magnus (1998–2014)** — ~950+ total citations across 3 papers | Markov chain is the correct generative model. Points are nearly i.i.d. Break points are 4x more important than regular points. |
| **O'Donoghue (2012)** | Single break overturned 18% of time. Double break = 93-97% certain. Information follows S-curve over match. |
| **Roberts & Streeter (2017)** — MIT Sloan | In-play odds >90% realized 91.5%. >95% realized 96.1%. Betfair converges to 5% of final by end of first set in 72% of matches. |
| **Šarčević et al. (2022)** — Cited 20 | Comprehensive review: taxonomy of point-based, paired-comparison, and ML approaches. |

### Tennis + ML

| Citation | Key Finding |
|---|---|
| **Wilkens (2021)** — Cited 88 | **Most important paper for your use case.** Markets do not fully incorporate shot-level information between scoring events. Pre-match models ceiling at ~72%. Calibration to market price is dominant accuracy driver. |
| **Quan, Chen & Chen (2026)** — IEEE Access | H-NHMC hybrid achieves **78.7% accuracy** — current SOTA. 3-stage: RF → Markov chain → hierarchical recursion. |
| **Wang & Drekic (2026)** — J. Sports Analytics | Pure Markov + ensembling hits ~70%, on par with ML. The combo is what matters. |
| **Lei et al. (2024)** — arXiv 2404.13300 | HMM detects momentum states lasting 2-3 points. XGBoost + SHAP shows momentum features have predictive power beyond score state. |

### Sports Betting ML

| Citation | Key Finding |
|---|---|
| **Walsh & Joshi (2024)** — ML with Applications, cited 23 | **Calibration-optimized models beat accuracy-optimized by 69.86% in returns.** This is the paper that tells you what metric to optimize. |
| **Montrucchio, Barbierato & Gatti (2026)** — MDPI Information | Ablation study: fused (market + stats) beats either alone. Market-implied probabilities systematically overestimate favorites — this miscalibration IS the edge. |
| **Terawong & Cliff (2024)** — arXiv 2401.06086 | XGBoost agent learned profitable in-play strategies on betting exchange simulator. Generalized beyond training patterns. |
| **Teles (2026)** — SSRN | Convex pooling of structural + market models reduces log-loss. Both sources contain complementary information. |
| **Galekwa et al. (2024)** — arXiv 2410.21484 | Systematic review of ML in sports betting: SVM, RF, NN across soccer, basketball, tennis, cricket. |
| **Franck, Verbeek & Nüesch (2013)** — Economica | 19.2% of matches offered arbitrage. Bookmaker prices are behavioral, not purely efficient. |

### Production Systems

| System | Architecture | Key Feature |
|---|---|---|
| **nflfastR (R package)** | XGBoost × 2 (EP → WP) | Shallow trees (max_depth ≤ 5). No calibration layer. Monotonicity constraints. Train weights down-weight distant plays. |
| **nflWAR** | XGBoost + Markov-ish state | Single model, not layered. Pre-trained models shipped as package data. |

### Momentum Debate

| Citation | Finding |
|---|---|
| **Klaassen & Magnus (2001)** — JASA | **No robust evidence for psychological momentum.** Points nearly i.i.d. Effects <1% per point. "Scoreboard momentum" explains apparent patterns. |
| **Klein Teeselink & van den Assem (2022)** | Momentum is mean reversion + narrative bias, not a real effect. |

---

## 10. Appendix: Benchmark Expectations

### Expected Performance (from literature)

| Metric | Expected Range | Source |
|---|---|---|
| Pre-match accuracy | 65-74% | Wilkens 2021 |
| After 1 set accuracy | 82-87% | Multiple studies |
| After 2 sets (Bo3) accuracy | 93-96% | Multiple studies |
| Best reported accuracy (hybrid) | 78.7% | Quan et al. 2026 |
| Pure Markov accuracy | ~70% | Wang & Drekic 2026 |
| Brier score (good) | < 0.18 | Walsh & Joshi 2024 |
| ECE (well-calibrated) | < 0.02 | Industry standard |
| Sharpe ratio (decent) | > 0.5 | — |
| Sharpe ratio (good) | > 1.0 | — |
| Sharpe ratio (great) | > 2.0 | — |

### Minimum Viable Data Requirements

| Milestone | Settled Matches | Can Do |
|---|---|---|
| Baseline (constant pre-match price) | 0 | Floor comparison |
| Markov chain | 0 | Structural prior, no training needed |
| Logistic regression | ~50 | Simple calibration check |
| GBM (any variant) | ~200 | First trainable models |
| All 288 model variants | ~500 | Full zoo training |
| Sequence models (LSTM/GRU) | ~1,000 | Temporal pattern learning |

### Implementation Order

```
Week 1-2:   Let ghost-trader run. Build API-Tennis WebSocket integration.
Week 3:     Validate API-Tennis alignment. Build baseline models (logistic, markov).
Week 4:     GBM tier. Feature engineering. Cross-validation pipeline.
Week 5:     Full model zoo. Calibration testing. Backtest.
Week 6:     Deploy to cloud. Start ghost trading with paper capital.
Week 7-10:  Iterate on thresholds, features, model selection.
Week 11+:   Live trading with real capital (small). Monitor and refine.
```
