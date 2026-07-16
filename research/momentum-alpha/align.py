"""Align court + market features per point, define target variable.

Produces final feature matrix for model training.
"""

import numpy as np
import pandas as pd

from data import get_home_away_market_tickers
from features.court_features import compute_court_features
from features.market_features import compute_market_features_at_points, compute_price_target


def build_match_dataset(points_df, ticks_df, markets_df, meta, hmm_model=None,
                        config=None, horizon_sec=30, threshold_cents=1):
    """Build aligned feature dataset for a single match.

    Returns (X_df, y_series, prices_entry, prices_exit) or (None, None, None, None).
    """
    if points_df.empty or ticks_df.empty:
        return None, None, None, None

    if len(points_df) < 20 or len(ticks_df) < 100:
        return None, None, None, None

    # court features
    court = compute_court_features(points_df, model=hmm_model,
                                   beta=config["ema"]["beta"] if config else 0.85,
                                   n_states=config["hmm"]["n_states"] if config else 3)

    # market tickers
    home_ticker, away_ticker = get_home_away_market_tickers(markets_df, meta)

    # market features
    market = compute_market_features_at_points(
        ticks_df, court, home_ticker,
        velocity_windows_sec=[10, 30]
    )

    # target
    y = compute_price_target(ticks_df, court, home_ticker,
                             horizon_sec=horizon_sec,
                             threshold_cents=threshold_cents)

    # compute entry/exit prices for backtest
    home_ticks = ticks_df[ticks_df["market_ticker"] == home_ticker].copy()
    if home_ticks.empty:
        home_ticks = ticks_df.copy()
    home_ticks = home_ticks.sort_values("ts")
    home_ticks["price"] = pd.to_numeric(home_ticks["price"], errors="coerce")
    home_ticks = home_ticks.dropna(subset=["price"])

    prices_entry = []
    prices_exit = []
    for i in range(len(court)):
        pt_ts = court.iloc[i]["ts_ms"]
        future_ts = pt_ts + (horizon_sec * 1000)
        prior = home_ticks[home_ticks["ts"] <= pt_ts]
        future = home_ticks[home_ticks["ts"] <= future_ts]
        if prior.empty or future.empty:
            prices_entry.append(np.nan)
            prices_exit.append(np.nan)
        else:
            prices_entry.append(prior.iloc[-1]["price"])
            prices_exit.append(future.iloc[-1]["price"])

    # merge
    court = court.reset_index(drop=True)
    market = market.reset_index(drop=True)
    y = y.reset_index(drop=True)

    X = pd.concat([court, market], axis=1)

    # select feature columns
    feature_cols = [
        "hmm_state", "momentum_raw", "momentum_ema",
        "serve_win_rate_home", "serve_win_rate_away",
        "break_rate_home", "break_rate_away",
        "score_diff_games", "score_diff_sets",
        "points_into_game", "is_break_point", "is_server_home",
        "price", "spread", "bid_ask_imbalance",
        "price_velocity_10s", "price_velocity_30s",
        "volume_velocity_30s", "trade_flow_imbalance",
    ]

    available = [c for c in feature_cols if c in X.columns]
    X = X[available].copy()
    for c in X.columns:
        X[c] = pd.to_numeric(X[c], errors="coerce")
    X = X.fillna(0)

    # drop rows with NaN target
    valid = y.notna() & y.apply(lambda v: isinstance(v, (int, float, np.integer, np.floating)))
    X = X[valid].reset_index(drop=True)
    y = y[valid].reset_index(drop=True).astype(int)
    prices_entry = [prices_entry[i] for i in range(len(valid)) if valid.iloc[i]]
    prices_exit = [prices_exit[i] for i in range(len(valid)) if valid.iloc[i]]

    if len(X) < 20:
        return None, None, None, None

    return X, y, prices_entry, prices_exit


def build_all_datasets(matches, hmm_model=None, config=None):
    """Build datasets for all matches.

    Returns (X_all, y_all, match_labels, prices_entry, prices_exit).
    """
    horizon = config["target"]["horizon_sec"][1] if config else 30
    threshold = config["target"]["threshold_cents"] if config else 1

    all_X = []
    all_y = []
    all_labels = []
    all_prices_entry = []
    all_prices_exit = []

    for points_df, ticks_df, markets_df, meta in matches:
        X, y, p_entry, p_exit = build_match_dataset(
            points_df, ticks_df, markets_df, meta,
            hmm_model=hmm_model, config=config,
            horizon_sec=horizon, threshold_cents=threshold,
        )
        if X is None:
            print(f"  skip {meta['match_ticker']}: insufficient data")
            continue

        all_X.append(X)
        all_y.append(y)
        all_labels.append(pd.Series([meta["match_ticker"]] * len(X)))
        all_prices_entry.extend(p_entry)
        all_prices_exit.extend(p_exit)
        print(f"  {meta['match_ticker']}: {len(X)} samples, "
              f"pos_rate={y.mean():.2f}")

    if not all_X:
        return None, None, None, None, None

    X_all = pd.concat(all_X, ignore_index=True)
    y_all = pd.concat(all_y, ignore_index=True)
    labels_all = pd.concat(all_labels, ignore_index=True)

    return X_all, y_all, labels_all, all_prices_entry, all_prices_exit
