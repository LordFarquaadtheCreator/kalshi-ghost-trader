# Research Modules (Go)

Exploratory data analysis on `kalshi_tennis.db`. Each module queries fresh
data on every run — safe to re-run daily as the scraper accumulates.

## Usage

```bash
# from repo root
go run ./research/go <module> [-db kalshi_tennis.db]

# list modules
go run ./research/go
```

## Modules

| Module | RQ | Question |
|---|---|---|
| `taker-flow` | RQ4 | Does signed trade flow predict forward price? |
| `empirical-hold` | RQ6+RQ11 | Empirical serve hold rates vs Markov assumption |
| `bp-overreaction` | RQ7 | Break conversion over-reaction (Betfair pattern)? |
| `mp-calibration` | RQ8 | Match-point market price vs empirical conversion |
| `pp-latency` | RQ12 | Point-to-price latency — edge window? |
| `depth-dynamics` | RQ9 | Multi-level orderbook depth vs forward volatility |

## Architecture

- `main.go` — subcommand dispatch, `-db` flag
- `db.go` — read-only SQLite connection, shared helpers
- One file per module, registers via `init()`
- All DB access read-only (`mode=ro`) — safe while scraper writes
- No output files — prints to stdout. Redirect with `> research/go/out/<module>.txt`

## Known Data Limitations

- **Taker flow one-directional**: Kalshi WS only reports sell-YES / buy-NO
  trades. No buy-YES / sell-NO in data. Signed volume always negative.
- **`is_match_point` / `is_set_point` columns all 0**: DB columns not
  populated. `mp-calibration` recomputes from score state using
  `algorithms.ClassifyPoint`.
- **`home_set_games` / `away_set_games` NULL**: Set tracking done by
  detecting set transitions in `mp-calibration`.
- **Doubles blind spot**: 92 doubles markets tracked, zero point data.
  All modules skip doubles (no score feed).

## Build

```bash
go build ./research/go/
go vet ./research/go/
```
