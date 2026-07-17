# ML Models Roadmap

What models we can train and deploy as trading strategies, organized by
data scale. Current data: 4 days, 470 matches, 56k points, 1.26M ticks,
686k trades.

## Scale projection

| Window | Matches | Points | Ticks | Trades |
|---|---|---|---|---|
| Now (4 days) | 470 | 56k | 1.26M | 686k |
| 2 weeks | ~1.6k | ~200k | ~4.4M | ~2.4M |
| 1 month | ~3.5k | ~420k | ~9.5M | ~5.2M |
| 3 months | ~10k | ~1.2M | ~28M | ~15M |

## Deployment pattern

```
Python (offline)                    Go (online)
─────────────────                   ──────────────────
research/ml/train_*.py              internal/algorithms/*.go
  → train sklearn/lightgbm           → load JSON weights at startup
  → export weights to JSON           → score features per point/tick
  → research/ml/models/*.json        → emit orders via OrderEmitter
```

Train offline in Python, export weights as JSON, load at runtime in Go.
Standard deploy pattern — no ML runtime in the live process.

## Viable now (470 matches, 56k points)

### M1. Serve-win probability model (logistic)
- **Target**: P(server wins point | series, score_state, is_break_point)
- **Features**: series, server, home_games, away_games, home_points, away_points, is_break_point, is_tiebreak
- **Model**: logistic regression (sklearn)
- **Why**: RQ6 showed global pServe=0.64 overestimates by 7pp. Per-context model fixes this.
- **Strategy**: feed calibrated pServe into Markov → better fair value → better edge detection
- **Effort**: 1 day
- **Data ready**: yes

### M2. Markov residual predictor (LightGBM)
- **Target**: `market_price - markov_fair_value` 60s forward
- **Features**: market_price, markov_fair_value, score_state, series, recent_vol, spread, time_to_close
- **Model**: LightGBM regression
- **Why**: direct tradeable signal — predicts where market will revert to
- **Strategy**: long YES when predicted residual > fee + buffer
- **Effort**: 2 days
- **Data ready**: yes (need to compute markov_fair_value per point)

### M3. Spike classifier (unsupervised)
- **Target**: cluster spikes into "informational" vs "over-reaction"
- **Features**: spike_magnitude, velocity, volume_in_window, score_context, spread_before_after
- **Model**: k-means / GMM
- **Why**: existing spike_reversion.py shows regular spikes negative, huge spikes (+30c) positive
- **Strategy**: only fade spikes classified as "over-reaction" cluster
- **Effort**: 1 day
- **Data ready**: yes

### M4. Set-outcome model (logistic)
- **Target**: P(server wins set | set_score, game_score, server, series)
- **Features**: games_home, games_away, points, server, series, sets_won
- **Model**: logistic regression
- **Why**: simpler than full match model, more samples per set
- **Strategy**: trade when model prob diverges from market by >fee
- **Effort**: 1 day
- **Data ready**: yes

## Viable at 2+ weeks (1.6k+ matches, 200k+ points)

### M5. Per-player serve model
- Logistic: `P(server wins point | player, series, surface, score_state)`
- Need ~30+ points per player for stable estimate
- At 2 weeks: top 200 players have 50+ points each
- Replaces global pServe with player-specific prior
- **Big win**: Isner wins 78% on serve, WTA doubles specialist 48%

### M6. Surface-conditioned models
- Clay vs hard vs grass hold rates differ 8-12pp
- 2 weeks gives enough per-surface sample
- Features: surface, series, player, score
- Strategy: surface-aware Markov → surface-aware fair value

### M7. Elo / Glicko player ratings
- Need cross-match player history (2 weeks = ~3 matches/player avg, marginal)
- Better at 1 month: ~6 matches/player
- Use as feature in serve model and match-win model

### M8. Order flow toxicity model (revisit RQ4)
- 2.4M trades at 2 weeks — enough for VPIN / signed-flow model
- Currently RQ4 fails because all trades one-directional (data issue)
- With more data we can identify missing side via orderbook reconstruction

### M9. Retrain momentum-alpha properly
- Existing infra in `research/momentum-alpha/`
- 470 matches too small; 1.6k workable
- Add Wilkens ensemble (N≥4 models), baseline "no-trade" benchmark

## Viable at 1+ month (3.5k+ matches, 420k+ points)

### M10. Sequence model on point-by-point (LSTM/GRU)
- 420k points is enough for a small LSTM / GRU
- Input: point sequence (server, scorer, point_type, score_state)
- Output: P(win match) at each point
- Wilkens got positive Sharpe at this scale with ensemble

### M11. Per-player comeback coefficients
- "Player X recovers from 0-40 at 8%, player Y at 1%"
- Need 50+ 0-40 situations per player → 1 month sufficient for top 100
- Direct upgrade to existing comeback040 strategy

### M12. Surface × player interaction terms
- "Alcaraz holds 72% on clay, 64% on hard"
- Need ~10 matches per player-surface combo
- 1 month gives that for top 50 players

### M13. Orderbook imbalance LSTM
- 9.5M ticks, ~5M orderbook events at 1 month
- Sequence model on (bid_depth, ask_depth, trade_flow, score) → 30s forward price

### M14. Cross-market arbitrage detector
- Kalshi vs Polymarket / Betfair
- Need 1 month of simultaneous data

## Viable at 3+ months (10k+ matches, 1.2M+ points)

### M15. Full transformer on match trajectory
- 1.2M points is real ML scale
- Transformer encoder on point sequence with player embeddings
- Output: P(win match) calibrated at every point
- Wilkens got Sharpe 0.5-1.0 with this scale + ensemble

### M16. Player embedding space
- Learn 64-dim embedding per player from point sequences
- Captures playing style (server vs returner, clutch, stamina)
- Used as features in all downstream models

### M17. Live market making RL
- 15M trades is enough replay data for off-policy RL
- State: orderbook, score, time, position
- Action: quote spread, size
- Reward: realized PnL - inventory penalty
- High effort, high reward — thesis-level play

### M18. Opponent-adjusted serve model
- "Isner wins 78% on serve vs avg, but 71% vs top-10 returners"
- Need 3+ matches per player-pair → 3 months sufficient for top 50

## Build priority

**Now (this week)**:
1. M1: Serve-win probability (logistic) — fixes biggest known bug
2. M2: Markov residual predictor (LightGBM) — direct tradeable signal
3. M4: Set-outcome model — easy, more samples

**Week 2 (when data doubles)**:
4. M5: Per-player serve model
5. M6: Surface-conditioned Markov
6. M9: Retrain momentum-alpha with ensemble

**Month 1**:
7. M10: Sequence model (LSTM) on point trajectory
8. M11: Player-specific comeback coefficients
9. M7: Elo ratings as features

**Month 3+**:
10. M15: Transformer on match trajectory
11. M17: RL market maker (only if other strategies validate)

## Honest caveat

Wilkens' thesis had ~1M points and got Sharpe 0.5-1.0 with ensemble.
We'll have 1M points at ~3 months. **Until then, simple calibrated models
(logistic, GBDT) will likely beat deep learning.** Deep models overfit
at small scale. The serve-win logistic on 56k points is probably more
accurate than a transformer on the same data.

The edge isn't in model complexity — it's in:
1. Fixing known calibration bugs (pServe, surface)
2. Per-player priors (Isner ≠ WTA doubles specialist)
3. Fast execution (pp-latency shows we have 17s window)
