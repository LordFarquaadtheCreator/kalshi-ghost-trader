"""Market features from Kalshi tick data.

Computes per-point market state:
- price: YES price for home player market
- spread: yes_ask - yes_bid
- price_velocity: price change over last N seconds
- volume_velocity: volume change over last N seconds
- trade_flow_imbalance: net taker_side buy vs sell
- bid_ask_imbalance: (bid_size - ask_size) / (bid_size + ask_size)
"""

import numpy as np
import pandas as pd


def compute_market_features_at_points(ticks_df, points_df, home_ticker,
                                       velocity_windows_sec=[10, 30]):
    """Compute market features aligned to each point timestamp.

    For each point, find latest tick before point timestamp.
    Compute velocity features over trailing window.

    ticks_df: all ticks for home player's market, sorted by ts
    points_df: points with ts_ms column
    home_ticker: market_ticker for home player

    Returns DataFrame aligned to points_df with market features.
    """
    if ticks_df.empty or points_df.empty:
        return pd.DataFrame()

    # filter to home market only
    home_ticks = ticks_df[ticks_df["market_ticker"] == home_ticker].copy()
    if home_ticks.empty:
        home_ticks = ticks_df.copy()  # fallback: use all

    home_ticks = home_ticks.sort_values("ts").reset_index(drop=True)
    home_ticks["ts_sec"] = home_ticks["ts"] // 1000

    # ensure numeric
    for col in ["price", "yes_bid", "yes_ask", "yes_bid_size", "yes_ask_size",
                "volume", "last_trade_size"]:
        if col in home_ticks.columns:
            home_ticks[col] = pd.to_numeric(home_ticks[col], errors="coerce")

    # build per-point features
    results = []
    for _, pt in points_df.iterrows():
        pt_ts = pt["ts_ms"]
        pt_ts_sec = pt_ts // 1000

        # latest tick at or before point timestamp
        prior = home_ticks[home_ticks["ts"] <= pt_ts]
        if prior.empty:
            results.append(_empty_market_features(velocity_windows_sec))
            continue

        latest = prior.iloc[-1]
        row = {
            "price": latest.get("price", np.nan),
            "yes_bid": latest.get("yes_bid", np.nan),
            "yes_ask": latest.get("yes_ask", np.nan),
            "spread": np.nan,
            "bid_ask_imbalance": np.nan,
            "volume": latest.get("volume", 0),
        }

        bid = latest.get("yes_bid", np.nan)
        ask = latest.get("yes_ask", np.nan)
        bid_size = latest.get("yes_bid_size", np.nan)
        ask_size = latest.get("yes_ask_size", np.nan)

        if pd.notna(bid) and pd.notna(ask):
            row["spread"] = ask - bid
        if pd.notna(bid_size) and pd.notna(ask_size) and (bid_size + ask_size) > 0:
            row["bid_ask_imbalance"] = (bid_size - ask_size) / (bid_size + ask_size)

        # velocity features: price change over trailing window
        for window in velocity_windows_sec:
            window_start = pt_ts - (window * 1000)
            window_ticks = prior[prior["ts"] >= window_start]
            if len(window_ticks) >= 2:
                first_price = window_ticks.iloc[0].get("price", np.nan)
                last_price = window_ticks.iloc[-1].get("price", np.nan)
                if pd.notna(first_price) and pd.notna(last_price):
                    row[f"price_velocity_{window}s"] = last_price - first_price
                else:
                    row[f"price_velocity_{window}s"] = 0.0

                # volume velocity
                first_vol = window_ticks.iloc[0].get("volume", 0)
                last_vol = window_ticks.iloc[-1].get("volume", 0)
                if pd.notna(first_vol) and pd.notna(last_vol):
                    row[f"volume_velocity_{window}s"] = last_vol - first_vol
                else:
                    row[f"volume_velocity_{window}s"] = 0.0
            else:
                row[f"price_velocity_{window}s"] = 0.0
                row[f"volume_velocity_{window}s"] = 0.0

        # trade flow imbalance: net buy vs sell in last 30s
        window_30 = prior[prior["ts"] >= pt_ts - 30000]
        if not window_30.empty and "taker_side" in window_30.columns:
            buys = (window_30["taker_side"] == "buy").sum()
            sells = (window_30["taker_side"] == "sell").sum()
            total = buys + sells
            row["trade_flow_imbalance"] = (buys - sells) / total if total > 0 else 0.0
        else:
            row["trade_flow_imbalance"] = 0.0

        results.append(row)

    return pd.DataFrame(results)


def _empty_market_features(velocity_windows_sec):
    row = {
        "price": np.nan, "yes_bid": np.nan, "yes_ask": np.nan,
        "spread": np.nan, "bid_ask_imbalance": np.nan, "volume": 0,
        "trade_flow_imbalance": 0.0,
    }
    for w in velocity_windows_sec:
        row[f"price_velocity_{w}s"] = 0.0
        row[f"volume_velocity_{w}s"] = 0.0
    return row


def compute_price_target(ticks_df, points_df, home_ticker, horizon_sec=30,
                         threshold_cents=1):
    """Compute target variable: will home YES price increase over horizon?

    For each point, compare price at point_ts vs price at point_ts + horizon.
    Label: 1 = price up, 0 = price down/flat (above threshold = up).

    Returns Series of labels aligned to points_df.
    """
    if ticks_df.empty or points_df.empty:
        return pd.Series(dtype=int)

    home_ticks = ticks_df[ticks_df["market_ticker"] == home_ticker].copy()
    if home_ticks.empty:
        home_ticks = ticks_df.copy()

    home_ticks = home_ticks.sort_values("ts").reset_index(drop=True)
    home_ticks["price"] = pd.to_numeric(home_ticks["price"], errors="coerce")
    home_ticks = home_ticks.dropna(subset=["price"])

    if home_ticks.empty:
        return pd.Series([np.nan] * len(points_df))

    labels = []
    for _, pt in points_df.iterrows():
        pt_ts = pt["ts_ms"]
        future_ts = pt_ts + (horizon_sec * 1000)

        # price at point time
        prior = home_ticks[home_ticks["ts"] <= pt_ts]
        future = home_ticks[home_ticks["ts"] <= future_ts]

        if prior.empty or future.empty:
            labels.append(np.nan)
            continue

        price_now = prior.iloc[-1]["price"]
        price_future = future.iloc[-1]["price"]
        delta = price_future - price_now

        if abs(delta) < threshold_cents / 100.0:
            labels.append(0)  # no meaningful move
        elif delta > 0:
            labels.append(1)  # price up
        else:
            labels.append(0)  # price down

    return pd.Series(labels)
