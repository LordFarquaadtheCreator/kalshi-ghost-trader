# Kalshi Tennis Ghost Trader — Design Document

> Ghost trading system tracking Kalshi tennis match markets in real-time via
> WebSocket, storing every price/trade/lifecycle message to PostgreSQL for
> algorithm testing. Optional API-Tennis WebSocket provides point-by-point
> score data. 18 pluggable strategies run simulated trades on every match.
> SvelteKit dashboard for monitoring. Future ML layer planned for model-based
> prediction and edge detection.

## Document Index

| Document | Description |
|---|---|
| [architecture.md](architecture.md) | System architecture, package layout, concurrency model, hosting (Linux Mint), deployment, monitoring, snapshots, backup, dashboard, tennis series |
| [data-schema.md](data-schema.md) | Data sources (Kalshi WS, Kalshi REST, API-Tennis WS, Kalshi live-data), PostgreSQL tables, cascade deletes, coverage classification, janitor, migrations |
| [strategies.md](strategies.md) | Strategy interfaces, order emission (QuotaGuard, KalshiOrderEmitter), Kelly sizing, Markov model (with full tiebreak), strategy catalog (18 strategies), backtest, price band analysis |
| [ml-roadmap.md](ml-roadmap.md) | Future ML vision: feature engineering, model zoo (~288 variants), training pipeline, calibration, mimicking detection, ghost trading engine, deployment |
| [research.md](research.md) | Research references (~20 papers), benchmark expectations, momentum debate |

## Current State

**What exists:**
- Go service collecting Kalshi market data (ticks, orderbook, lifecycle) for 12 tennis series
- API-Tennis WebSocket scraper for point-by-point score data
- PostgreSQL storage with single-writer batched inserts
- 18 pluggable trading strategies running simulated trades on every match
- Hierarchical Markov chain model with full tiebreak support
- Backtest engine replaying historical data through strategies
- Price band analysis tool
- SvelteKit dashboard (matches, orders, strategies, system metrics)
- Deployed on Linux Mint box via systemd, 24/7

**What's planned (see ml-roadmap.md):**
- Python ML inference engine for model-based predictions
- ~288 model variants (GBM × feature sets × calibration)
- Daily training pipeline with grouped k-fold cross-validation
- Calibration-optimized model selection (ECE primary metric)
- Convex pool ensemble blending Markov + market + ML
