# Research References

## Core Tennis Win Probability

| Citation | Key Finding |
|---|---|
| **Klaassen & Magnus (1998–2014)** — ~950+ total citations | Markov chain is the correct generative model. Points are nearly i.i.d. Break points are 4x more important than regular points. |
| **O'Donoghue (2012)** | Single break overturned 18% of time. Double break = 93-97% certain. Information follows S-curve over match. |
| **Roberts & Streeter (2017)** — MIT Sloan | In-play odds >90% realized 91.5%. >95% realized 96.1%. Betfair converges to 5% of final by end of first set in 72% of matches. |
| **Šarčević et al. (2022)** — Cited 20 | Comprehensive review: taxonomy of point-based, paired-comparison, and ML approaches. |

## Tennis + ML

| Citation | Key Finding |
|---|---|
| **Wilkens (2021)** — Cited 88 | **Most important paper for this use case.** Markets do not fully incorporate shot-level information between scoring events. Pre-match models ceiling at ~72%. Calibration to market price is dominant accuracy driver. |
| **Quan, Chen & Chen (2026)** — IEEE Access | H-NHMC hybrid achieves **78.7% accuracy** — current SOTA. 3-stage: RF → Markov chain → hierarchical recursion. |
| **Wang & Drekic (2026)** — J. Sports Analytics | Pure Markov + ensembling hits ~70%, on par with ML. The combo is what matters. |
| **Lei et al. (2024)** — arXiv 2404.13300 | HMM detects momentum states lasting 2-3 points. XGBoost + SHAP shows momentum features have predictive power beyond score state. |

## Sports Betting ML

| Citation | Key Finding |
|---|---|
| **Walsh & Joshi (2024)** — ML with Applications, cited 23 | **Calibration-optimized models beat accuracy-optimized by 69.86% in returns.** This is the paper that tells you what metric to optimize. |
| **Montrucchio, Barbierato & Gatti (2026)** — MDPI Information | Ablation study: fused (market + stats) beats either alone. Market-implied probabilities systematically overestimate favorites — this miscalibration IS the edge. |
| **Terawong & Cliff (2024)** — arXiv 2401.06086 | XGBoost agent learned profitable in-play strategies on betting exchange simulator. Generalized beyond training patterns. |
| **Teles (2026)** — SSRN | Convex pooling of structural + market models reduces log-loss. Both sources contain complementary information. |
| **Galekwa et al. (2024)** — arXiv 2410.21484 | Systematic review of ML in sports betting: SVM, RF, NN across soccer, basketball, tennis, cricket. |
| **Franck, Verbeek & Nüesch (2013)** — Economica | 19.2% of matches offered arbitrage. Bookmaker prices are behavioral, not purely efficient. |

## Production Systems

| System | Architecture | Key Feature |
|---|---|---|
| **nflfastR (R package)** | XGBoost × 2 (EP → WP) | Shallow trees (max_depth ≤ 5). No calibration layer. Monotonicity constraints. Train weights down-weight distant plays. |
| **nflWAR** | XGBoost + Markov-ish state | Single model, not layered. Pre-trained models shipped as package data. |

## Momentum Debate

| Citation | Finding |
|---|---|
| **Klaassen & Magnus (2001)** — JASA | **No robust evidence for psychological momentum.** Points nearly i.i.d. Effects <1% per point. "Scoreboard momentum" explains apparent patterns. |
| **Klein Teeselink & van den Assem (2022)** | Momentum is mean reversion + narrative bias, not a real effect. |

## Benchmark Expectations

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
