# Kalshi Tennis Markets — Complete Structural Research

> Compiled from live Kalshi public API queries (2026-07-12/13) + official Kalshi docs.
> All JSON examples are real responses from `https://external-api.kalshi.com/trade-api/v2`.

---

## 1. SERIES TICKERS

`Tennis` tag has 100+ series. **Core match-winner series** (one binary market per player, "Will X win?"):

| # | Series Ticker | Title | Frequency | Competitions | Settlement Source |
|---|---|---|---|---|---|
| 1 | `KXATPMATCH` | ATP Tennis Match | custom | ATP main tour men's singles (Bastad, Gstaad, Umag) | ATP (atptour.com) |
| 2 | `KXWTAMATCH` | WTA Tennis Match | custom | WTA main tour women's singles (Athens, Iasi) | WTA (wtatennis.com) |
| 3 | `KXITFMATCH` | ITF Men's Match | custom | ITF men's singles (M15 Gubbio) | ITF + Flashscore + Fox Sports + ESPN |
| 4 | `KXITFWMATCH` | ITF Women's Match | custom | ITF women's singles (W75 Vitoria Gasteiz) | ITF + ESPN + Flashscore + Fox Sports |
| 5 | `KXATPCHALLENGERMATCH` | Challenger ATP | custom | ATP Challenger men's (Bunschoten, Cordenons) | ATP (atptour.com) |
| 6 | `KXWTACHALLENGERMATCH` | Challenger WTA | custom | WTA 125K women's (Kitzbuhel, Istanbul 2) | WTA (wtatennis.com) |
| 7 | `KXTENNISEXHIBITION` | Tennis Exhibition Match | custom | Exhibition matches | (varies) |
| 8 | `KXCHALLENGERMATCH` | Challenger ATP (alt/legacy) | custom | ATP Challenger (duplicate, same title) | ATP |

**Notes:**
- `KXTENNISEXHIBITION` returned **zero open events** at query time. More granular exhibition tickers exist: `KXEXHIBITIONMEN`, `KXEXHIBITIONWOMEN`.
- `KXCHALLENGERMATCH` is older/parallel ticker alongside `KXATPCHALLENGERMATCH`.

**Related (non-core) match-level series** for in-play/derivative markets on same matches:
- Set/game winners: `KXATPSETWINNER`, `KXWTASETWINNER`, `KXATPS1GWINNER`…`KXATPS5GWINNER`, `KXATPGWINNER`, `KXATPANYSET`
- Totals/spreads/exact score: `KXATPGTOTAL`, `KXATPGAMESPREAD`, `KXATPGSPREAD`, `KXATPEXACTMATCH`, `KXWTAEXACTMATCH`, `KXATPGAMETOTAL`
- Aces/tiebreaks: `KXATPACES`, `KXWTAACES`, `KXATPTIEBREAK`
- Doubles: `KXATPDOUBLES`, `KXWTADOUBLES`, `KXITFDOUBLES`, `KXITFWDOUBLES`
- Team/event matches: `KXUNITEDCUPMATCH`, `KXDAVISCUPMATCH`, `KXSIXKINGSMATCH`, `KXSIXKINGSSEMI`, `KXSIXKINGSQUARTER`, `KXBATTLEOFSEXES`

**Futures/tournament-winner series** (different structure): `KXATPMEN`, `KXWTA`, `KXATP`, `KXATPGRANDSLAM`, `KXWTAGRANDSLAM`, `KXGRANDSLAM`, `KXATPFINALS`, `KXWTAFINALS`, `KXUNITEDCUP`, `KXDAVISCUP`, plus per-tournament tickers (`KXAOMEN`, `KXWIMMEN`, `KXFOMEN`, etc.).

**Source:** `GET /series?tags=Tennis&limit=100`; `GET /events?series_ticker=KX*` (queries 1–9).

---

## 2. EVENT STRUCTURE

### Event object fields (tennis match)

Every tennis match = **event** with exactly **2 child markets** (one per player). From `GET /events`:

| Field | Value / Meaning |
|---|---|
| `event_ticker` | e.g. `KXATPMATCH-26JUL13SANDAN` (format below) |
| `series_ticker` | `KXATPMATCH` / `KXWTAMATCH` / etc. |
| `title` | `"Sanchez Jover vs Daniel"` — player1 vs player2 (full names) |
| `sub_title` | `"Sanchez Jover vs Daniel (Jul 13)"` — same + scheduled date |
| `category` | `"Sports"` (always) |
| `collateral_return_type` | `"MECNET"` (always) |
| `mutually_exclusive` | `true` (always — exactly one player wins) |
| `strike_period` | `""` (empty for tennis) |
| `product_metadata` | `{ "competition": "ATP Bastad", "competition_scope": "Game" }` |
| `settlement_sources` | Array of `{name, url}` — ATP/WTA for main tours; ITF+media for ITF |
| `available_on_brokers` | bool (varies per match) |
| `last_updated_ts` | ISO timestamp |

### event_ticker format

```
{SERIES_TICKER}-{YY}{MON}{DD}{P1ABBREV}{P2ABBREV}
```

- `YY` = 2-digit year (`26` = 2026)
- `MON` = 3-letter month uppercase (`JUL`, `FEB`, `JAN`)
- `DD` = 2-digit zero-padded day (`13`, `07`)
- `P1ABBREV` = first 3 letters of player 1's surname (`SAN` = Sanchez Jover, `DAN` = Daniel)
- `P2ABBREV` = first 3 letters of player 2's surname

Real examples (captured 2026-07-12):
- `KXATPMATCH-26JUL13SANDAN` → Sanchez Jover vs Daniel, Jul 13
- `KXATPMATCH-26JUL13FAUDAM` → Faurel vs Damas, Jul 13
- `KXATPMATCH-26JUL13COLSKA` → Collignon vs Skatov, Jul 13
- `KXWTAMATCH-26JUL13TAUHIB` → Tauson vs Hibino, Jul 13
- `KXITFMATCH-26JUL13DELTAI` → Dell'elba vs Tailleu, Jul 13
- `KXITFWMATCH-26JUL13LAZREV` → Lazarenko vs Reva, Jul 13
- `KXATPCHALLENGERMATCH-26JUL13FORHUA` → Forger vs Huang, Jul 13
- `KXWTACHALLENGERMATCH-26JUL13TIKBEN` → Tikhonova vs Bennemann, Jul 13

User example `KXATPMATCH-26JAN27SHESIN` decodes: ATP match, Jan 27, `SHE` vs `SIN` (Shelbayh vs Sinitsyn, real Jan 2026 qual match).

### product_metadata fields

```json
"product_metadata": {
    "competition": "ATP Bastad",
    "competition_scope": "Game"
}
```

- `competition`: tournament name. ATP → `"ATP Bastad"`, `"ATP Gstaad"`, `"ATP Umag"`. WTA → `"WTA Athens"`, `"WTA Iasi"`. ITF → just `"ITF"` (specific event like "M15 Gubbio" / "W75 Vitoria Gasteiz" only in `rules_primary`). Challenger ATP → `"ATP Challenger Bunschoten"`, `"ATP Challenger Cordenons"`. WTA Challenger → `"WTA 125K Kitzbuhel"`, `"WTA 125K Istanbul 2"`.
- `competition_scope`: always `"Game"` for match-level markets.

### sub_title format

`"{Player1 surname} vs {Player2 surname} ({Mon} {Day})"` — e.g. `"Sanchez Jover vs Daniel (Jul 13)"`. Date = scheduled match date.

### settlement_sources

| Series | Sources |
|---|---|
| KXATPMATCH | `[{"name":"ATP","url":"https://www.atptour.com/"}]` |
| KXWTAMATCH | `[{"name":"WTA","url":"https://www.wtatennis.com/"}]` |
| KXITFMATCH | ITF, Flashscore, Fox Sports, ESPN (4 sources) |
| KXITFWMATCH | ITF, ESPN, Flashscore, Fox Sports (4 sources) |
| KXATPCHALLENGERMATCH | ATP (atptour.com) |
| KXWTACHALLENGERMATCH | WTA (wtatennis.com) |

### Markets per event

**Always exactly 2 markets** per tennis match event — one "Will Player1 win?" and one "Will Player2 win?". Confirmed across ATP, WTA, ITF men, ITF women, Challenger ATP, Challenger WTA (every event queried returned 2 markets).

**Source:** API queries 1, 4–9 (events); `GET /markets?event_ticker=KXATPMATCH-26JUL13SANDAN` (returned exactly 2).

---

## 3. MARKET STRUCTURE

### Market ticker format

```
{EVENT_TICKER}-{PLAYER_ABBREV}
```

Suffix = **3-letter abbreviation of player this market represents**. Market answers "Will {this player} win?"

Examples:
- `KXATPMATCH-26JUL13SANDAN-SAN` → "Will Carlos Sanchez Jover win?" (SAN = Sanchez Jover)
- `KXATPMATCH-26JUL13SANDAN-DAN` → "Will Taro Daniel win?" (DAN = Daniel)
- `KXATPMATCH-26JUL13FAUDAM-FAU` → "Will Thomas Faurel win?"
- `KXWTAMATCH-26JUL13TAUHIB-TAU` → "Will Clara Tauson win?"
- `KXITFWMATCH-26JUL13KARFER-KAR` → "Will Zoziya Kardava win?"
- `KXATPCHALLENGERMATCH-26JUL13FORHUA-FOR` → "Will Abel Forger win?"

**Identify which player market represents:** read `yes_sub_title` (or `custom_strike.tennis_competitor` UUID). `rules_primary` also names player explicitly.

### yes_sub_title vs no_sub_title

For tennis match-winner markets, **both `yes_sub_title` and `no_sub_title` = SAME player name** — the player that market represents:

```json
"yes_sub_title": "Carlos Sanchez Jover",
"no_sub_title":  "Carlos Sanchez Jover"
```

- `yes_sub_title` = the player (YES = this player wins)
- `no_sub_title` = same player (NO = this player does NOT win, i.e. opponent wins)

Convention: each market is self-contained "Will X win?" binary. Opponent name NOT in subtitles; only in `rules_primary` and event `title`.

### rules_primary (tennis)

Template:
> "If {Player Full Name} wins the {Match} professional tennis match in the {Year} {Competition} {Round} after a ball has been played, then the market resolves to Yes."

Real examples:
- "If Taro Daniel wins the Sanchez Jover vs Daniel professional tennis match in the 2026 ATP Bastad Qualification Final after a ball has been played, then the market resolves to Yes."
- "If Clara Tauson wins the Tauson vs Hibino professional tennis match in the 2026 WTA Athens Round Of 32 after a ball has been played, then the market resolves to Yes."
- "If Vito Dell'elba wins the Dell'elba vs Tailleu professional tennis match in the 2026 M15 Gubbio Round of 16 after a ball has been played, then the market resolves to Yes."

**Round** embedded in rules_primary: "Qualification Final", "Round Of 32", "Round of 16", "Final", etc.

### rules_secondary — TWO variants

**Variant A (ATP / WTA / Challenger — "fair price"):**
> "The following market refers to the {Match} … after a ball has been played. If the match does not occur (signaled by a ball being played) due to a player injury, walkover, forfeiture, or any other cancellation (all before the match starts), **the market will resolve to a fair price in accordance with the rules**. If this match is postponed or delayed, the market will remain open and close after the rescheduled match has finished (within two weeks)."

**Variant B (ITF men & women — explicit $0.50 / No):**
> "… If the match does not occur … (all before the match starts), **all markets will resolve to $0.50**. If a player **withdraws or forfeits after a match has started, that player will resolve to No**. If this match is postponed or delayed, the market will remain open and close after the rescheduled match has finished (within two weeks)."

**Key difference:** ITF markets have explicit $0.50 cancellation rule + "withdrawal after start → No" rule. ATP/WTA/Challenger use vague "fair price" determination.

### custom_strike field

```json
"custom_strike": {
    "tennis_competitor": "54316482-6c32-43cf-a2bc-777a1dfd44d7"
}
```

- `tennis_competitor`: **UUID** uniquely identifying player. Stable per-player ID across all matches (Taro Daniel = `54316482-...`, Carlos Sanchez Jover = `1af52ad0-...`). Use to track player across multiple events/markets.
- `strike_type`: `"structured"` (always for tennis)

**Source:** API query 2 (markets), `GET /markets?event_ticker=...`, WTA/ITF/Challenger market queries.

---

## 4. TIMING FIELDS

All times ISO-8601 UTC. From real active ATP market (`KXATPMATCH-26JUL13SANDAN-DAN`):

| Field | Value | Meaning |
|---|---|---|
| `created_time` | `2026-07-12T15:30:56Z` | When Kalshi created market |
| `open_time` | `2026-07-12T15:35:00Z` | When trading opened (~5 min after created) |
| `occurrence_datetime` | `2026-07-13T14:30:00Z` | **Scheduled match start time** |
| `expected_expiration_time` | `2026-07-13T14:30:00Z` | Equals occurrence_datetime (forecasted resolution) |
| `close_time` | `2026-07-27T11:30:00Z` | **Latest** trading close — ~14 days after match (postponement buffer) |
| `expiration_time` | `2026-07-27T11:30:00Z` | Deprecated; equals latest_expiration_time |
| `latest_expiration_time` | `2026-07-27T11:30:00Z` | Latest possible expiry (= close_time) |
| `can_close_early` | `true` | Always true for tennis |
| `early_close_condition` | `"This market will close and expire after a winner is declared."` | Always this text |
| `settlement_timer_seconds` | `60` | 60-second dispute window after determination |

### Key timing relationships

- **`occurrence_datetime` = scheduled match start time.** NOT market open time. Confirmed: equals `expected_expiration_time`, is day/time match scheduled (e.g. Jul 13 14:30 UTC for Jul 13 match).
- **`open_time` ≈ `created_time` + ~5 minutes.** Markets created and opened shortly after, typically day before or day of match.
- **`close_time` set ~14 DAYS in future** from match (e.g. match Jul 13 → close_time Jul 27). This is **postponement buffer**, NOT actual close. Per `rules_secondary`: "If this match is postponed or delayed, the market will remain open and close after the rescheduled match has finished (within two weeks)."
- **Market closes EARLY** (well before `close_time`) when winner declared, because `can_close_early=true` + `early_close_condition` triggers. See settled example where `close_time` moved to actual match-end time.
- **`expected_expiration_time` can be before `close_time`** — confirmed by Kalshi docs FAQ: expected_expiration = when event resolves; close_time = automatic fallback. For tennis they set expected_expiration = match start (earliest outcome could be known is shortly after start).

### Settled match timing (real example: `KXATPMATCH-26JUL12PRASAI-PRA`)

| Field | Value |
|---|---|
| `open_time` | `2026-07-11T20:05:00Z` |
| `occurrence_datetime` | `2026-07-12T18:00:00Z` (scheduled start) |
| `expected_expiration_time` | `2026-07-12T18:00:00Z` |
| `close_time` | `2026-07-12T17:35:14Z` ← **moved to actual match end** (early close) |
| `latest_expiration_time` | `2026-07-26T15:00:00Z` (original 2-week buffer, unchanged) |
| `settlement_ts` | `2026-07-12T17:37:19Z` (~2 min after close) |
| `status` | `finalized` |

Market created with close_time ~2 weeks out. When match ended at 17:35:14, Kalshi **moved close_time to actual end** and settled ~2 minutes later.

**Source:** API query 2 (active markets), API query 11 (settled markets), Kalshi docs `market_lifecycle`.

---

## 5. LIFECYCLE

### Status progression for tennis matches

Per Kalshi docs (`https://docs.kalshi.com/getting_started/market_lifecycle`) + observed data:

```
initialized → active → closed → determined → finalized
                   ↘ inactive ↗
```

| Status | When (for tennis) |
|---|---|
| `initialized` | Created but before `open_time` (brief; ~5 min) |
| `active` | Trading open. **Stays active DURING match** (in-play). Does NOT change when match starts. |
| `inactive` | Temporarily paused by exchange (rare) |
| `closed` | `close_time` passes OR **early-close triggers** (winner declared). No new orders. |
| `determined` | Result set (`result` = "yes"/"no"). 60s settlement timer runs. |
| `disputed` | Result challenged (rare) |
| `amended` | Re-determined after dispute |
| `finalized` | Settlement complete, positions paid out. **Terminal.** `settlement_ts` populated. |

### REST filter mapping

| Filter value (`status=`) | Matches |
|---|---|
| `unopened` | `initialized` |
| `open` | `active` |
| `paused` | `inactive` |
| `closed` | Any past close_time, not yet finalized (includes `closed`, `determined`, `disputed`, `amended`) |
| `settled` | `finalized` |

### Time between close and settlement

From settled example: `close_time` = 17:35:14Z, `settlement_ts` = 17:37:19Z → **~2 minutes**. `settlement_timer_seconds` = 60s; total elapsed including processing ~125s.

### When does `result` get populated?

`result` = `""` (empty) while market is `active`/`closed`. Set to `"yes"`, `"no"`, or `"scalar"` at `determined` stage, persists through `finalized`.

### `result` values observed

| `result` | Meaning | `settlement_value_dollars` |
|---|---|---|
| `""` | Not yet determined (active/closed) | (absent) |
| `"yes"` | Player this market represents WON | `"1.0000"` |
| `"no"` | Player this market represents LOST | `"0.0000"` |

Real settled pair (same event `KXATPMATCH-26JUL12PRASAI`):
- `KXATPMATCH-26JUL12PRASAI-PRA` (Prado Angelo): `result: "yes"`, `settlement_value_dollars: "1.0000"`, `last_price: "0.99"`
- `KXATPMATCH-26JUL12PRASAI-SAI` (Sanchez Izquierdo): `result: "no"`, `settlement_value_dollars: "0.0000"`, `last_price: "0.01"`

**Source:** API query 11 (settled markets), Kalshi market_lifecycle docs.

---

## 6. MATCH DETECTION

### How to know a match has STARTED

- **`occurrence_datetime` = scheduled match start time.** Use `now > occurrence_datetime` as signal match has (scheduled) started.
- **Market `status` does NOT change when match starts.** Remains `active` throughout match. No "in-play" status in REST API.
- **Cannot reliably detect in-play from REST data alone.** Market stays `active` from `open_time` until winner declared (early close). Need external live-score feed (ATP/WTA/Flashscore) to know actual ball-in-play status, or watch for `close_time` moved earlier.

### Does market status change when match starts? When it ends?

- **Starts:** No status change. Stays `active`.
- **Ends:** Yes. When winner declared, `can_close_early` + `early_close_condition` trigger: `close_time` moved to actual end time, status → `closed` → `determined` (result set) → `finalized`.

### Detecting in-play

Not directly from Kalshi REST. Indirect signals:
1. `now` between `occurrence_datetime` and updated (earlier) `close_time` → likely in-play or just finished.
2. Watch `market_lifecycle_v2` WebSocket channel for `close_date_updated` events (close_time moved earlier = match ending/ended).
3. Price converging to 0.99/0.01 while still `active` can indicate near-certain outcome mid-match, but not definitive.

**Source:** Kalshi market_lifecycle docs; observed active markets with `occurrence_datetime` in future and `status: "active"`.

---

## 7. VOLUME / LIQUIDITY

| Metric | Typical Range (observed) |
|---|---|
| `liquidity_dollars` | **Always `"0.0000"`** — DEPRECATED (per Kalshi docs: "will always return 0.0000") |
| `volume_fp` (contracts) | ATP main tour, early/qual: 145–6,632. ATP settled (popular): up to 483,074. WTA: 0–57. ITF men/women: typically 0 (no trades). Challenger ATP: 0–2,205. Challenger WTA: 0. |
| `volume_24h_fp` | Same as volume_fp for newly opened markets |
| `open_interest_fp` | ATP: 143–6,486 (active); up to 286,488 (settled popular). WTA: 0–57. ITF: 0. Challenger: 0–1,084. |
| `notional_value_dollars` | `"1.0000"` (always — $1 per contract) |
| `price_level_structure` | `"linear_cent"` (1¢ ticks) |
| `price_ranges` | `[{"start":"0.0000","end":"1.0000","step":"0.0100"}]` |

### Are tennis markets liquid enough for algorithm testing?

- **ATP/WTA main tour: YES**, especially main-draw rounds (R32/R16/QF+) and known players. Settled ATP qual finals showed 160K–483K contracts. Active early ATP markets show 2K–6K contracts and real two-sided order books (e.g. Tabur vs Rodionov: yes_ask 0.58, yes_bid 0.57, sizes 2,797 / 77).
- **ITF men/women: NO** — almost universally zero volume and zero open interest at listing. Only seeded/known players get any flow. Useful for structural testing, not fill-quality testing.
- **Challenger ATP/WTA: MARGINAL** — some ATP Challenger matches had 1K–2K contracts; WTA Challenger was 0.
- **Order book depth:** Even active ATP markets often have thin best-bid/ask sizes (e.g. 21–125 contracts at best bid) but larger asks (5K–10K). Slippage on sizeable orders will be material.

**Source:** API queries 2, 11; WTA/ITF/Challenger market queries.

---

## 8. FULL JSON EXAMPLES

### 8a. Complete EVENT object (ATP, active)

```json
{
    "available_on_brokers": true,
    "category": "Sports",
    "collateral_return_type": "MECNET",
    "event_ticker": "KXATPMATCH-26JUL13SANDAN",
    "last_updated_ts": "2026-07-12T15:30:56.195063Z",
    "mutually_exclusive": true,
    "product_metadata": {
        "competition": "ATP Bastad",
        "competition_scope": "Game"
    },
    "series_ticker": "KXATPMATCH",
    "settlement_sources": [
        {
            "name": "ATP",
            "url": "https://www.atptour.com/"
        }
    ],
    "strike_period": "",
    "sub_title": "Sanchez Jover vs Daniel (Jul 13)",
    "title": "Sanchez Jover vs Daniel"
}
```

### 8b. Complete MARKET object (player-win, active) — `KXATPMATCH-26JUL13SANDAN-DAN`

```json
{
    "can_close_early": true,
    "close_time": "2026-07-27T11:30:00Z",
    "created_time": "2026-07-12T15:30:56.172741Z",
    "custom_strike": {
        "tennis_competitor": "54316482-6c32-43cf-a2bc-777a1dfd44d7"
    },
    "early_close_condition": "This market will close and expire after a winner is declared.",
    "event_ticker": "KXATPMATCH-26JUL13SANDAN",
    "expected_expiration_time": "2026-07-13T14:30:00Z",
    "expiration_time": "2026-07-27T11:30:00Z",
    "expiration_value": "",
    "last_price_dollars": "0.7700",
    "latest_expiration_time": "2026-07-27T11:30:00Z",
    "liquidity_dollars": "0.0000",
    "market_type": "binary",
    "no_ask_dollars": "0.2400",
    "no_bid_dollars": "0.2300",
    "no_sub_title": "Taro Daniel",
    "notional_value_dollars": "1.0000",
    "occurrence_datetime": "2026-07-13T14:30:00Z",
    "open_interest_fp": "850.64",
    "open_time": "2026-07-12T15:35:00Z",
    "previous_price_dollars": "0.0000",
    "previous_yes_ask_dollars": "0.0000",
    "previous_yes_bid_dollars": "0.0000",
    "price_level_structure": "linear_cent",
    "price_ranges": [
        { "end": "1.0000", "start": "0.0000", "step": "0.0100" }
    ],
    "result": "",
    "rules_primary": "If Taro Daniel wins the Sanchez Jover vs Daniel professional tennis match in the 2026 ATP Bastad Qualification Final after a ball has been played, then the market resolves to Yes.",
    "rules_secondary": "The following market refers to the Sanchez Jover vs Daniel professional tennis match in the 2026 ATP Bastad Qualification Final after a ball has been played. If the match does not occur (signaled by a ball being played) due to a player injury, walkover, forfeiture, or any other cancellation (all before the match starts), the market will resolve to a fair price in accordance with the rules. If this match is postponed or delayed, the market will remain open and close after the rescheduled match has finished (within two weeks).",
    "settlement_timer_seconds": 60,
    "status": "active",
    "strike_type": "structured",
    "ticker": "KXATPMATCH-26JUL13SANDAN-DAN",
    "title": "Will Taro Daniel win the Sanchez Jover vs Daniel: Qualification Final match?",
    "updated_time": "2026-07-12T15:35:00.07304Z",
    "volume_24h_fp": "850.64",
    "volume_fp": "850.64",
    "yes_ask_dollars": "0.7700",
    "yes_ask_size_fp": "713.93",
    "yes_bid_dollars": "0.7600",
    "yes_bid_size_fp": "931.30",
    "yes_sub_title": "Taro Daniel"
}
```

### 8c. Complete SETTLED MARKET object (winner) — `KXATPMATCH-26JUL12PRASAI-PRA`

```json
{
    "can_close_early": true,
    "close_time": "2026-07-12T17:35:14Z",
    "created_time": "2026-07-11T20:01:01.506973Z",
    "custom_strike": {
        "tennis_competitor": "327b3e8c-0a87-4818-8e6e-4c99af461e2e"
    },
    "early_close_condition": "This market will close and expire after a winner is declared.",
    "event_ticker": "KXATPMATCH-26JUL12PRASAI",
    "expected_expiration_time": "2026-07-12T18:00:00Z",
    "expiration_time": "2026-07-26T15:00:00Z",
    "expiration_value": "Juan Carlos Prado Angelo",
    "last_price_dollars": "0.9900",
    "latest_expiration_time": "2026-07-26T15:00:00Z",
    "liquidity_dollars": "0.0000",
    "market_type": "binary",
    "no_ask_dollars": "1.0000",
    "no_bid_dollars": "0.0000",
    "no_sub_title": "Juan Carlos Prado Angelo",
    "notional_value_dollars": "1.0000",
    "occurrence_datetime": "2026-07-12T18:00:00Z",
    "open_interest_fp": "286488.00",
    "open_time": "2026-07-11T20:05:00Z",
    "previous_price_dollars": "0.4500",
    "previous_yes_ask_dollars": "0.4600",
    "previous_yes_bid_dollars": "0.4300",
    "price_level_structure": "linear_cent",
    "price_ranges": [
        { "end": "1.0000", "start": "0.0000", "step": "0.0100" }
    ],
    "result": "yes",
    "rules_primary": "If Juan Carlos Prado Angelo wins the Prado Angelo vs Sanchez Izquierdo professional tennis match in the 2026 ATP Umag Qualification Final after a ball has been played, then the market resolves to Yes.",
    "rules_secondary": "The following market refers to the Prado Angelo vs Sanchez Izquierdo professional tennis match in the 2026 ATP Umag Qualification Final after a ball has been played. If the match does not occur (signaled by a ball being played) due to a player injury, walkover, forfeiture, or any other cancellation (all before the match starts), the market will resolve to a fair price in accordance with the rules. If this match is postponed or delayed, the market will remain open and close after the rescheduled match has finished (within two weeks).",
    "settlement_timer_seconds": 60,
    "settlement_ts": "2026-07-12T17:37:19.498576Z",
    "settlement_value_dollars": "1.0000",
    "status": "finalized",
    "strike_type": "structured",
    "ticker": "KXATPMATCH-26JUL12PRASAI-PRA",
    "title": "Will Juan Carlos Prado Angelo win the Prado Angelo vs Sanchez Izquierdo: Qualification Final match?",
    "updated_time": "2026-07-12T17:37:19.554315Z",
    "volume_24h_fp": "483057.38",
    "volume_fp": "483074.49",
    "yes_ask_dollars": "1.0000",
    "yes_ask_size_fp": "0.00",
    "yes_bid_dollars": "0.0000",
    "yes_bid_size_fp": "0.00",
    "yes_sub_title": "Juan Carlos Prado Angelo"
}
```

### 8d. Companion SETTLED MARKET (loser) — `KXATPMATCH-26JUL12PRASAI-SAI`

```json
{
    "ticker": "KXATPMATCH-26JUL12PRASAI-SAI",
    "event_ticker": "KXATPMATCH-26JUL12PRASAI",
    "status": "finalized",
    "result": "no",
    "settlement_ts": "2026-07-12T17:37:19.498576Z",
    "settlement_value_dollars": "0.0000",
    "close_time": "2026-07-12T17:35:14Z",
    "expected_expiration_time": "2026-07-12T18:00:00Z",
    "expiration_time": "2026-07-26T15:00:00Z",
    "latest_expiration_time": "2026-07-26T15:00:00Z",
    "occurrence_datetime": "2026-07-12T18:00:00Z",
    "open_time": "2026-07-11T20:05:00Z",
    "expiration_value": "Juan Carlos Prado Angelo",
    "last_price_dollars": "0.0100",
    "yes_sub_title": "Nikolas Sanchez Izquierdo",
    "no_sub_title": "Nikolas Sanchez Izquierdo",
    "volume_fp": "438485.77",
    "open_interest_fp": "241508.88"
}
```

Note: `expiration_value` on BOTH markets of this event = "Juan Carlos Prado Angelo" (winner's name), even on loser's market. Populated at settlement.

### 8e. ITF women's market (Variant B rules_secondary) — `KXITFWMATCH-26JUL13KARFER-KAR`

```json
{
    "ticker": "KXITFWMATCH-26JUL13KARFER-KAR",
    "event_ticker": "KXITFWMATCH-26JUL13KARFER",
    "yes_sub_title": "Zoziya Kardava",
    "no_sub_title": "Zoziya Kardava",
    "rules_primary": "If Zoziya Kardava wins the Kardava vs Ferlito professional tennis match in the 2026 W75 Vitoria Gasteiz Round of 16 after a ball has been played, then the market resolves to Yes.",
    "rules_secondary": "The following market refers to the Kardava vs Ferlito professional tennis match in the 2026 W75 Vitoria Gasteiz Round of 16 after a ball has been played. If the match does not occur (signaled by a ball being played) due to a player injury, walkover, forfeiture, or any other cancellation (all before the match starts), all markets will resolve to $0.50. If a player withdraws or forfeits after a match has started, that player will resolve to No. If this match is postponed or delayed, the market will remain open and close after the rescheduled match has finished (within two weeks).",
    "occurrence_datetime": "2026-07-13T17:00:00Z",
    "open_time": "2026-07-12T23:13:00Z",
    "close_time": "2026-07-27T11:00:00Z",
    "expected_expiration_time": "2026-07-13T17:00:00Z",
    "can_close_early": true,
    "early_close_condition": "This market will close and expire after a winner is declared.",
    "settlement_timer_seconds": 60,
    "status": "active",
    "volume_fp": "0.00",
    "open_interest_fp": "0.00"
}
```

---

## 9. PRACTICAL SUMMARY FOR A TRACKER

### Detecting matches (start/end) from API

| Question | Answer |
|---|---|
| Match scheduled start time | `occurrence_datetime` on either market (also = `expected_expiration_time`) |
| Market opened for trading | `open_time` (status flips `initialized`→`active`, no WS event) |
| Has match (scheduled) started? | `now > occurrence_datetime` AND `status == "active"` |
| Has match ended? | `status` ∈ {`closed`, `determined`, `finalized`} OR `close_time` < original 2-week buffer (close_time moved earlier via `close_date_updated` WS event) |
| Who won? | Market with `result == "yes"` (its `yes_sub_title` / `custom_strike.tennis_competitor` is winner) |
| In-play detection | **Not available via REST.** Use external live scores, or infer from price near 0.99/0.01 while active. |

### Recommended polling strategy

1. **Discover matches:** `GET /events?series_ticker=KXATPMATCH&status=open` (repeat for each of 8 series). Use `with_nested_markets=true` if need market statuses in one call.
2. **Get markets:** `GET /markets?event_ticker={event_ticker}` → 2 markets. Parse `yes_sub_title` + `custom_strike.tennis_competitor` for player identity.
3. **Track lifecycle:** Subscribe to `market_lifecycle_v2` WebSocket for `close_date_updated` (match ending), `determined` (result set), `settled` (finalized). Or poll `GET /markets?series_ticker=...&status=closed` and `status=settled`.
4. **Match start:** Use `occurrence_datetime`; do NOT rely on status changes (none occur at start).

### Key gotchas

- `close_time` is **2-week postponement buffer**, not match end. Don't use as "match end time" for active markets.
- `liquidity_dollars` is **always 0** (deprecated). Use `volume_fp`, `open_interest_fp`, and order-book sizes instead.
- `yes_sub_title` == `no_sub_title` for tennis (both = market's player). Don't assume no_sub_title is opponent.
- ITF markets have **different cancellation rules** ($0.50 refund + withdrawal-after-start → No) vs ATP/WTA ("fair price").
- `expiration_value` populated at settlement with **winner's name** on both markets of event.
- `occurrence_datetime` = `expected_expiration_time` for tennis (both = scheduled start).
- `series` endpoint does NOT filter by `ticker` param; use `tags=Tennis` or `category=Sports` and filter client-side.

---

## SOURCES

**Kalshi public API (live queries, 2026-07-12/13):**
- `GET /events?series_ticker=KXATPMATCH&status=open&limit=5`
- `GET /markets?series_ticker=KXATPMATCH&status=open&limit=10`
- `GET /markets?event_ticker=KXATPMATCH-26JUL13SANDAN`
- `GET /events?series_ticker=KXWTAMATCH&status=open&limit=5`
- `GET /events?series_ticker=KXITFMATCH&status=open&limit=5`
- `GET /events?series_ticker=KXITFWMATCH&status=open&limit=5`
- `GET /events?series_ticker=KXATPCHALLENGERMATCH&status=open&limit=5`
- `GET /events?series_ticker=KXWTACHALLENGERMATCH&status=open&limit=5`
- `GET /events?series_ticker=KXTENNISEXHIBITION&status=open&limit=5` (empty)
- `GET /events?series_ticker=KXATPMATCH&limit=10` (all statuses)
- `GET /markets?series_ticker=KXATPMATCH&status=settled&limit=3`
- `GET /markets?series_ticker=KXWTAMATCH&status=open&limit=4`
- `GET /markets?series_ticker=KXITFWMATCH&status=open&limit=4`
- `GET /markets?series_ticker=KXATPCHALLENGERMATCH&status=open&limit=4`
- `GET /markets?series_ticker=KXITFMATCH&status=open&limit=2`
- `GET /markets?series_ticker=KXWTACHALLENGERMATCH&status=open&limit=2`
- `GET /series?tags=Tennis&limit=100`
- `GET /series?ticker=KXATPMATCH` (returns all series; ticker filter not supported)

**Kalshi documentation:**
- `https://docs.kalshi.com/getting_started/market_lifecycle` — statuses, transitions, time fields, settlement
- `https://docs.kalshi.com/api-reference/market/get-market` — Market schema (OpenAPI)
- `https://docs.kalshi.com/api-reference/market/get-series-list` — series filtering
- `https://docs.kalshi.com/websockets/market-&-event-lifecycle` — WS lifecycle events
- `https://kalshi-public-docs.s3.amazonaws.com/contract_terms/TENNISGAMEDIFF.pdf` — tennis contract terms (cancellation/withdrawal rules)

**Web:**
- `https://kalshi.com/tag/tennis` — tennis market hub (returned 429; title confirmed via search)
- `https://kalshi.com/markets/kxatpmatch/atp-tennis-match/kxatpmatch-26feb07sacoco` — example match page (via search)
- `https://nexteventhorizon.substack.com/p/now-you-can-bet-on-single-tennis-matches-kalshi` — background on Kalshi tennis match launch
