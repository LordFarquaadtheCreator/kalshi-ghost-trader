# Momentum-Alpha vs Wilkens (2021) — Concerns

Source: Wilkens, S. (2021). *Sports prediction and betting models in the machine learning age: The case of tennis.* Journal of Sports Analytics 7, 99–117.

## Paper (Wilkens 2021)

Pre-match, binary: favorite wins? 39k ATP/WTA matches, 2010–2019.

Features: bookmaker odds (avg, max, spread), gender, series, round, log rank diff, log rank-points diff, age diff, preferred hand, home adv, surface adv, past duels, 6mo momentum.

Models: LR, NN, RF, GBM, SVM + baseline (favorite always wins) + bookmaker-implied challenger. 3yr sliding train → 1yr predict, 7 rounds. 5-fold CV.

Money mgmt: fixed, fixed %, fixed expected return, Kelly (capped 1%), variance-opt. Ensembles N=2..5.

Findings:
- ~70% accuracy ceiling. No ML beats odds-implied.
- Odds dominate feature importance. Player/match features negligible.
- Returns mostly negative. Best: ensemble N=4 favorite ~10% raw, 0.10 risk-adj; ensemble N=5 longshot ~34% raw, 0.22 risk-adj but bets on 0.2% of matches.
- High vol kills raw returns. Weak business case.

## Our model (momentum-alpha)

In-play, post-point: predict market price move over 10/30/60s. 10 matches extracted (Kalshi Challenger/ITF), ~150–220 pts each, ~4k–52k ticks.

Features: court (HMM state, momentum EMA, serve win rates, break rates, score diffs, break point, server) + market (price, spread, price velocity 10s/30s, volume velocity, trade flow imbalance, bid-ask imbalance).

Models: XGBoost, LightGBM. No ensemble, no baseline, no odds-implied challenger.

Backtest: $1000, 5% per trade, 1¢ fee, min conf 0.55. No risk-adjusted metric in config.

## Comparison

| Axis | Paper | Ours | Gap |
|---|---|---|---|
| Problem | pre-match winner | in-play price move | different problem, not directly comparable |
| Data | 39k matches | 10 matches | severe. ML won't generalize |
| Info source | bookmaker odds | market microstructure (price/flow) | Kalshi price IS the odds — risk of rediscovering efficiency |
| Models | 5 + ensembles | 2 boosted trees | missing paper's most promising approach (ensembles) |
| Baseline | favorite-always-wins + odds-implied | none | can't claim edge without benchmark |
| Money mgmt | 5 strategies + Kelly capped | fixed 5% | no Kelly, no sensitivity test |
| Eval | log-loss, Brier, AUC, calibration, ROI raw + risk-adj | backtest only, no metrics listed | missing risk-adj — paper's key warning |
| Train/test | sliding 3yr/1yr, 7 rounds | unspecified | no walk-forward defined |

## Concerns

1. **Sample size.** 10 matches vs 39k. Overfitting near-certain. Need 100s minimum before trusting any backtest.
2. **No baseline.** Paper's strongest finding: ML doesn't beat odds-implied. On Kalshi, market price = implied prob. Must add naive baseline (e.g. price stays flat, or price drifts toward 1.0/result) and beat it.
3. **No ensemble.** Paper's only positive returns came from ensembles N≥4. Single XGB/LGBM likely insufficient.
4. **No risk-adjusted metric.** Paper: raw returns misleading, vol huge. Add Sharpe-like + max drawdown.
5. **Market efficiency risk.** Paper says odds encompass most info. Our price/velocity features may just re-extract same info → no edge after fees.
6. **Feature overlap.** Paper found player features (hand, age, surface, duels) negligible. Our court features (serve win rate, break rate, score diff) likely similar fate vs market price. Worth feature importance check before going further.
7. **No walk-forward split.** Config has no train/val/test definition. Paper's sliding window is the right pattern.

## Recommendations

Before more modeling:
- Add baseline: predict no move (price stays) and odds-implied drift. Beat both.
- Add ensemble config (avg/vote across XGB + LGBM + maybe LR).
- Define walk-forward split in config (per-match leave-one-out given n=10).
- Add risk-adj metric + max drawdown to backtest output.
- Run feature importance early. Drop negligible court features if market dominates — matches paper finding.
