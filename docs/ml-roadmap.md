# ML Roadmap

Future ML vision. Not yet implemented. Based on research from ~20 papers on
tennis win probability, betting market efficiency, and sports prediction ML.

## Architecture

```
                          ┌──────────────────────────────────────┐
                          │        Cloud VM (24/7 runner)         │
                          │                                       │
  Kalshi ──WS/Signal──▶   │  ┌──────────┐    ┌───────────────┐   │
    WebSocket             │  │ Go Ghost │    │ Python ML     │   │
                          │  │ Trader   │◄──►│ Inference     │   │
  API-Tennis ──WS──▶     │  │ (ticks)  │    │ Engine        │   │
    Real-Time              │  │          │    │ (predictions) │   │
                          │  └─────┬────┘    └───────┬───────┘   │
                          │  ┌─────▼──────────────────▼───────┐   │
                          │  │      PostgreSQL DB             │   │
                          │  └─────▲───────────────────────────┘   │
                          │  ┌─────┴────────────┐                  │
                          │  │ Offline Trainer   │                  │
                          │  │ (cron: daily)     │                  │
                          │  └──────────────────┘                  │
                          └──────────────────────────────────────┘
```

| Process | Language | Role |
|---|---|---|
| `ghost-trader` | Go | WebSocket feeds, REST scans, tick storage, API-Tennis scraper (existing) |
| `ml-engine` | Python | Feature engineering → model inference → trade decisions (new) |
| `trainer` | Python | Daily: reload data, train all model permutations, evaluate, deploy best |

## Feature Engineering

### Score-State Features (from points table)

Markov chain baseline (Klaassen & Magnus 1998–2014, verified by Wang & Drekic 2026):

```
P_win_game(a, b, serving, p, q):
  # Standard DTMC with absorbing states at game won/lost

P_win_set(A, B, a, b, P_game):
  # At 6-6: full tiebreak Markov chain (first to 7, win by 2,
  #   serve alternates 1-2-2-1-1-2-2-1...)
  # Mid-tiebreak: uses current TB score + correct serve alternation

P_win_match(S, A, B, P_set):
  # Best-of-3 (first to 2 sets)
```

Features derived:
- `markov_win_prob` — P(win match) from current score
- `markov_game_prob` — P(win current game)
- `markov_set_prob` — P(win current set)
- `markov_point_importance` — change in win prob from winning vs losing this point

Score state indicators: sets_won, games_won/lost, game_score, is_break_point,
is_set_point, is_match_point, is_tiebreak, serving, serving_for_match/set,
up_break, double_break_up, down_break, double_break_down, score_state_id.

Within-match dynamics (Lei et al. 2024 — HMM + XGBoost for momentum):
last_5/10_points_won, points_streak, serves_held_consecutive, return_games_won.

### Market Features (from ticks table)

Current state: mid_price, price, spread, log_spread, bid/ask_depth,
book_imbalance, volume, open_interest.

Price dynamics (rolling windows): MA_10/100, std_10/100, velocity,
acceleration, min/max_50, volume_delta, oi_delta.

Market vs model gap: `market_minus_markov` — the edge signal for residual models.

### Pre-Match Features (external lookup)

player_elo, opponent_elo, elo_diff, serve_win_career (surface-specific),
return_win_career, surface one-hot, tier one-hot, is_bo5, match_round.

### Feature Set Permutations

| Set | Features | Rationale |
|---|---|---|
| S1: Score only | Score-state + pre-match. No market. | Pure statistical model. |
| S2: Market only | Current market + dynamics. No score. | Market microstructure baseline. |
| S3: Fused | Score + market + pre-match. Everything. | Highest accuracy. Risk: mimics market. |
| S4: Residual | `market_minus_markov` as target. | Directly predicts edge. Learns market biases. |
| S5: Markov + Market | Markov prob + market features. No raw score. | Lighter than full fused. |

## Model Zoo

### Tier 1 — Baselines

| Model | Feature Set | Why |
|---|---|---|
| Constant: pre-match market price | — | Floor. If you can't beat opening line, no edge. |
| Logistic Regression | S1 | Simplest calibrated model. |
| Markov Chain (closed-form) | S1 | Structural truth. No training. Full tiebreak model. ~70% accuracy. |

### Tier 2 — Gradient Boosted Trees

| Model | Feature Sets | Why |
|---|---|---|
| LightGBM | S1, S3, S4, S5 | Production gold standard (nflfastR). Fast, handles categoricals. |
| XGBoost | S1, S3, S4 | Slightly more accurate than LightGBM. Used in Lei et al. 2024. |
| CatBoost | S1, S3 | Best categorical handling. Online/incremental learning. |
| Random Forest | S1, S3 | Simple, SHAP-interpretable. Used in SOTA H-NHMC paper. |

### Tier 3 — Sequence Models (later phase, data-heavy)

| Model | Feature Set | When |
|---|---|---|
| LSTM | S1 over point sequences | 500+ matches. Captures temporal patterns. |
| GRU | S1 over point sequences | Simpler than LSTM, similar performance. |
| Transformer | — | **Skip.** Literature finds no benefit over GBM. State space too small. |

### Tier 4 — Ensembles

| Model | Why |
|---|---|
| Convex Pool: w×Markov + (1-w)×Market | Learn single weight w. Simple, interpretable. |
| Stacked Ensemble: top 3 → Logistic meta-model | Production standard. |
| Average Ensemble: top 5 uncorrelated | Reduces variance. |

### Calibration Methods

| Method | When |
|---|---|
| Platt scaling | Default for all models. Parametric. |
| Isotonic regression | Only with 500+ validation samples. Non-parametric. |
| Temperature scaling | Quick fix for NN/LSTM. Single parameter. |
| None | Baseline comparison. |

### Total Permutations

~288: 14 model classes × 5 feature sets × 4 calibration methods + 2 sequence models + 3 ensembles + 3 baselines.

## Training Pipeline

### Cross-Validation

**Match-level grouped k-fold, NEVER random shuffle.** Shuffle leaks future
information from same match into training (Wilkens 2021; Walsh & Joshi 2024).

Temporal holdout: most recent 2 weeks as final test set. Never inspect until
all model selection done.

### Training Script

```
train.py
  ├── load_data()           # SQL → pandas
  ├── engineer_features()   # markov probs, rolling windows
  ├── split_data()          # temporal + group k-fold
  ├── run_model_zoo()       # all permutations
  ├── train_ensembles()     # best-of-N convex pool, stacked
  ├── select_best_models()  # rank by ECE + log-loss + Sharpe
  ├── calibrate_holdout()   # Platt on holdout
  └── deploy()              # save top-3 to models/
```

### Model Run Logging

All variants logged to `model_runs` table with: val/test log_loss, brier,
ECE, AUC, accuracy, Sharpe, num_matches, num_examples, top_features,
market_correlation (detect mimicking), model_path, deployed flag.

## Calibration & Evaluation

### Why Calibration Trumps Accuracy

Walsh & Joshi 2024: calibration-optimized models beat accuracy-optimized by
**69.86% in average returns** across NBA seasons 2014-2019.

Betting profit depends on correct probability estimates, not correct
classifications.

| Metric | Priority | Target |
|---|---|---|
| ECE | 1st | < 0.02 |
| Log-loss | 2nd | Lower is better |
| Brier score | 3rd | < 0.18 |
| Sharpe (ghost) | 4th | > 1.0 good, > 2.0 great |
| AUC-ROC | 5th | > 0.80 |
| Accuracy | Last | ~70-75% is literature ceiling |

### Mimicking Detection

When market price is a feature, model may copy market rather than find edges.

Detection: `corr(predicted_prob, market_price) > 0.95` → mimicking.
SHAP: if `market_price` dominates importance (>50%) → mimicking.

Mitigation:
1. Ablation: always train S1 alongside fused. If S1 ≈ S3, use S1.
2. Residual approach: S4 directly predicts gap, not outcome.
3. Convex pool: `p_final = w × p_score + (1-w) × p_market`. w < 0.5 = model adds info.
4. Feature restriction: use `markov_win_prob` as only market-proximate feature.

## Ghost Trading Engine (future)

### Entry Conditions

- `abs(gap) > entry_threshold` (learned from validation)
- `spread < max_spread` (default 0.05)
- `time_until_close > 60s`
- NOT already in position
- Model confidence interval does NOT include market price

### Exit Conditions

- `market_price >= 0.95` (profit taken)
- `market_price <= 0.05` (loss cut)
- `abs(gap) < exit_threshold` (gap closed)
- Match ended (settlement)
- `max_hold_time` exceeded (default 60 min)

### Position Sizing

```
flat_base = 10% of available capital
scaled = flat_base × abs(gap) / entry_threshold
capped = min(scaled, max_single_bet)  // 25% of capital
```

## Deployment (future ML)

| Component | Technology |
|---|---|
| ML inference | Python 3.11 + ONNX runtime |
| Model training | Python 3.11 (LightGBM, XGBoost, sklearn, shap, optuna) |
| Scheduler | systemd timers or cron (daily at 4 AM) |
| Monitoring | Daily P&L report, alert on error rates |

### Minimum Viable Data

| Milestone | Settled Matches | Can Do |
|---|---|---|
| Baseline (pre-match price) | 0 | Floor comparison |
| Markov chain | 0 | Structural prior, no training |
| Logistic regression | ~50 | Simple calibration check |
| GBM (any variant) | ~200 | First trainable models |
| All 288 model variants | ~500 | Full zoo training |
| Sequence models (LSTM/GRU) | ~1,000 | Temporal pattern learning |
