# Momentum Alpha

Cross-domain momentum model: on-court tennis momentum → Kalshi market price prediction.

Based on [Capturing Momentum (Lei et al., 2024)](https://arxiv.org/html/2404.13300v1) — extended with Kalshi market microstructure features.

## Mission

Build on-court momentum model from point-by-point data. Use it to predict Kalshi market price moves. Find trading edge.

## Setup

```bash
conda create -n momentum-alpha python=3.11 -y
conda activate momentum-alpha
pip install -r requirements.txt
brew install libomp  # macOS only, for xgboost
```

## Extract Data

One-time extraction from SQLite to JSON files. Avoids repeated DB queries.

```bash
python extract_data.py ../../snapshot_for_charts.db
```

Outputs `data/extracted/{match_ticker}.json` + `index.json`.

## Run Pipeline

```bash
python run.py
```

Pipeline:
1. Load extracted JSON data
2. Train HMM (pooled across all matches) — 3 momentum states
3. Build aligned feature datasets (court + market features per point)
4. Train + evaluate 6 models (baseline / momentum_only / combined × xgboost / lightgbm)
5. SHAP feature importance on combined model
6. Sobol sensitivity analysis
7. Backtest simulated trades
8. Save results to `results/`

## Architecture

```
extract_data.py          # SQLite -> JSON extraction
config.yaml              # all hyperparameters
run.py                   # main pipeline runner

data/
  __init__.py            # load_match, load_all_matches, market ticker mapping

features/
  court_features.py      # HMM momentum, EMA, serve win %, break rate, score state
  market_features.py     # price velocity, spread, volume, trade flow, price target

align.py                 # timestamp join points <-> ticks, target definition

models.py                # baseline / momentum_only / combined, XGBoost + LightGBM
evaluate.py              # ROC, confusion matrix, SHAP, Sobol, results table
backtest.py              # simulate trades, PnL, hit rate, Sharpe, drawdown
```

## Feature Sets

### Court (from `points` table)
- `hmm_state` — HMM hidden state (0/1/2 = momentum regime)
- `momentum_raw` — HMM posterior-weighted momentum [-1, +1]
- `momentum_ema` — EMA-smoothed momentum (paper eq 7, beta=0.85)
- `serve_win_rate_home/away` — cumulative serve win rate
- `break_rate_home/away` — cumulative break rate
- `score_diff_games` — home_games - away_games
- `score_diff_sets` — home_set_games - away_set_games
- `points_into_game` — point number within current game
- `is_break_point` — 0/1
- `is_server_home` — 1 if home player serving

### Market (from `ticks` table)
- `price` — home player YES price at point time
- `spread` — yes_ask - yes_bid
- `bid_ask_imbalance` — (bid_size - ask_size) / (bid_size + ask_size)
- `price_velocity_10s/30s` — price change over trailing window
- `volume_velocity_30s` — volume change over trailing window
- `trade_flow_imbalance` — net taker buy vs sell in last 30s

## Target Variable

Binary: will home player YES price increase by >= 1 cent over next 30 seconds after point scored?

## Model Variants

| Variant | Features | Purpose |
|---|---|---|
| baseline | market only | market microstructure baseline |
| momentum_only | court only | paper replication (on-court momentum) |
| combined | court + market | does momentum add alpha? |

## Evaluation

- Leave-one-match-out cross-validation (GroupKFold by match_ticker)
- Metrics: accuracy, AUC, F1, precision, recall
- SHAP feature importance (paper section 5.2)
- Sobol sensitivity (paper section 6.2)
- Backtest: simulated trades with fees, position sizing, PnL

## Results

Results saved to `results/`:
- `model_comparison.csv` — all model metrics
- `shap_importance.csv` — SHAP feature rankings
- `sobol_sensitivity.csv` — Sobol first-order indices
- `backtest_results.csv` — trading simulation metrics
- `shap_combined_xgboost.png` — SHAP summary plot
- `shap_combined_xgboost_bar.png` — SHAP bar chart
- `summary.json` — pipeline summary

## Data Limitations

Snapshot DB (`snapshot_for_charts.db`) has no `orderbook_events` or `lifecycle_events` tables. Market microstructure features limited to tick-level data. For orderbook imbalance features, extract from `kalshi_tennis.db` instead.

API-Tennis points are thinner than JeffSackmann data (no ace, double fault, rally length, winner type). HMM observation vector is smaller than paper's.

## Paper Differences

| Paper | This implementation |
|---|---|
| JeffSackmann point-by-point CSVs | API-Tennis points via SQLite |
| Momentum → point winner prediction | Momentum → market price direction |
| No market data | Kalshi ticks (price, volume, orderbook) |
| Static post-match | Live + historical |
| HMM → EMA → XGBoost → LightGBM+SHAP → Sobol | Same pipeline, extended features |
