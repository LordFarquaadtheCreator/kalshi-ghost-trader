"""Data loader for extracted match JSON files."""

import json
from pathlib import Path

import pandas as pd


def load_config(path="config.yaml"):
    import yaml
    with open(path) as f:
        return yaml.safe_load(f)


def load_match(filepath):
    """Load a single extracted match JSON file."""
    with open(filepath) as f:
        data = json.load(f)

    points = pd.DataFrame(data["points"])
    ticks = pd.DataFrame(data["ticks"])
    markets = pd.DataFrame(data["markets"])

    if not points.empty:
        points["ts_ms"] = points["ts_ms"].astype("int64")
        points = points.sort_values("ts_ms").reset_index(drop=True)

    if not ticks.empty:
        ticks["ts"] = ticks["ts"].astype("int64")
        ticks = ticks.sort_values("ts").reset_index(drop=True)

    meta = {
        "match_ticker": data["match_ticker"],
        "home_player": data["home_player"],
        "away_player": data["away_player"],
        "tournament": data["tournament"],
        "surface": data["surface"],
        "category": data["category"],
        "pt_first_ts": data["pt_first_ts"],
        "pt_last_ts": data["pt_last_ts"],
    }

    return points, ticks, markets, meta


def load_all_matches(extracted_dir="data/extracted"):
    """Load all matches from extracted directory.

    Returns list of (points_df, ticks_df, markets_df, meta_dict).
    """
    extracted = Path(extracted_dir)
    index_path = extracted / "index.json"

    with open(index_path) as f:
        index = json.load(f)

    matches = []
    seen_tickers = set()
    for entry in index:
        ticker = entry["match_ticker"]
        if ticker in seen_tickers:
            continue
        seen_tickers.add(ticker)
        filepath = extracted / Path(entry["file"]).name
        if not filepath.exists():
            continue
        points, ticks, markets, meta = load_match(filepath)
        matches.append((points, ticks, markets, meta))

    return matches


def split_market_ticks_by_player(ticks, markets):
    """Split ticks into per-player YES market data.

    Kalshi match-winner markets: two tickers per event, one per player.
    Each has a YES side. Player 1 YES price = P(player 1 wins).
    Player 2 YES price = P(player 2 wins). Should sum to ~1.0.

    Returns dict: player_name -> ticks_df for that player's market.
    """
    result = {}
    for _, m in markets.iterrows():
        ticker = m["market_ticker"]
        player = m["player_name"]
        player_ticks = ticks[ticks["market_ticker"] == ticker].copy()
        if not player_ticks.empty:
            result[player] = player_ticks
    return result


def get_home_away_market_tickers(markets, meta):
    """Map home/away player to their market tickers.

    markets has player_name. meta has home_player/away_player.
    Returns (home_ticker, away_ticker).
    """
    home_player = meta["home_player"]
    away_player = meta["away_player"]

    home_ticker = None
    away_ticker = None

    for _, m in markets.iterrows():
        if home_player and home_player in m["player_name"]:
            home_ticker = m["market_ticker"]
        elif away_player and away_player in m["player_name"]:
            away_ticker = m["market_ticker"]

    # fallback: first market = home, second = away
    if not home_ticker and len(markets) > 0:
        home_ticker = markets.iloc[0]["market_ticker"]
    if not away_ticker and len(markets) > 1:
        away_ticker = markets.iloc[1]["market_ticker"]

    return home_ticker, away_ticker
