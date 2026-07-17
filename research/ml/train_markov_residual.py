#!/usr/bin/env python3
"""Train Markov residual predictor (M2).

LightGBM regression: predict (market_price - markov_fair_value) 60s forward.
Direct tradeable signal — predicts where market will revert to.

Exports model + feature config to JSON for Go runtime loading.
Uses lightgbm's native JSON format.
"""
import json
import sqlite3
from pathlib import Path

import lightgbm as lgb
import numpy as np
import pandas as pd
from sklearn.metrics import mean_squared_error, r2_score

DB = Path(__file__).resolve().parents[2] / "kalshi_tennis.db"
OUT_DIR = Path(__file__).resolve().parent / "models"
OUT_DIR.mkdir(parents=True, exist_ok=True)

POINT_MAP = {"0": 0, "15": 1, "30": 2, "40": 3, "A": 4}
SERIES_MAP = {
    "KXATPMATCH": 0, "KXATPCHALLENGERMATCH": 1, "KXITFMATCH": 2,
    "KXITFWMATCH": 3, "KXWTAMATCH": 4, "KXWTACHALLENGERMATCH": 5,
}
FORWARD_WINDOW_MS = 60_000


def point_to_num(s):
    return POINT_MAP.get(s, 4)


def markov_game_win(p_serve, hp, ap, server_is_home, is_tiebreak):
    """Probability home wins current game via point recursion."""
    if is_tiebreak:
        return 0.5
    if hp >= 4 and hp - ap >= 2:
        return 1.0
    if ap >= 4 and ap - hp >= 2:
        return 0.0
    if hp == ap and hp >= 3:  # deuce
        p = p_serve if server_is_home else (1 - p_serve)
        return p * p * 1.0 / (1 - 2 * p * (1 - p))
    if hp >= 4 and ap < 4:  # advantage home
        p = p_serve if server_is_home else (1 - p_serve)
        return p + (1 - p) * markov_game_win(p_serve, 3, 3, server_is_home, False)
    if ap >= 4 and hp < 4:  # advantage away
        p = p_serve if server_is_home else (1 - p_serve)
        return (1 - p) * markov_game_win(p_serve, 3, 3, server_is_home, False)
    # regular point
    p = p_serve if server_is_home else (1 - p_serve)
    # advance to next point
    return p * markov_game_win(p_serve, hp + 1, ap, server_is_home, False) + \
           (1 - p) * markov_game_win(p_serve, hp, ap + 1, server_is_home, False)


def markov_set_win(p_serve, gh, ga, server, p_home_game, is_tiebreak):
    """Probability home wins current set."""
    if gh >= 6 and gh - ga >= 2:
        return 1.0
    if ga >= 6 and ga - gh >= 2:
        return 0.0
    if gh == 6 and ga == 6:
        return 0.5  # crude tiebreak
    if gh >= 7:
        return 1.0
    if ga >= 7:
        return 0.0
    # next game
    # server alternates: game (gh+ga) even -> server 1 starts, etc.
    # simplified: use p_home_game as constant
    return p_home_game * markov_set_win(p_serve, gh + 1, ga, 3 - server, 1 - p_home_game + 0.5 * p_home_game, False) + \
           (1 - p_home_game) * markov_set_win(p_serve, gh, ga + 1, 3 - server, 1 - p_home_game + 0.5 * p_home_game, False)


def markov_match_win(sets_h, sets_a, p_home_set):
    """Probability home wins match (best of 3)."""
    if sets_h >= 2:
        return 1.0
    if sets_a >= 2:
        return 0.0
    return p_home_set * markov_match_win(sets_h + 1, sets_a, 1 - p_home_set) + \
           (1 - p_home_set) * markov_match_win(sets_h, sets_a + 1, 1 - p_home_set)


def markov_fair_value(sets_h, sets_a, gh, ga, hp, ap, server, is_tb, p_serve=0.64):
    """Fair YES price for home player from Markov model."""
    p_home_game = markov_game_win(p_serve, hp, ap, server == 1, is_tb)
    p_home_set = markov_set_win(p_serve, gh, ga, server, p_home_game, is_tb)
    p_home_match = markov_match_win(sets_h, sets_a, p_home_set)
    return p_home_match


def load_data(conn):
    """Load points + nearest market price for each point."""
    # Load points
    pts_q = """
    SELECT p.match_ticker, p.ts_ms, p.set_number, p.game_number,
           p.server, p.scorer, p.home_points, p.away_points,
           p.home_games, p.away_games,
           COALESCE(p.home_set_games, 0) as hsg,
           COALESCE(p.away_set_games, 0) as asg,
           p.is_tiebreak, p.is_break_point,
           e.series_ticker
    FROM points p
    JOIN events e ON p.match_ticker = e.event_ticker
    WHERE p.ts_ms IS NOT NULL
    ORDER BY p.match_ticker, p.ts_ms
    """
    pts = pd.read_sql_query(pts_q, conn)
    if pts.empty:
        return pts

    # Compute sets won from set_games (cumulative completed sets)
    # hsg/asg are final games in completed sets — sets_won = count of sets where won >= 6
    # Actually hsg is "final games in completed sets before this one" — need to count sets won
    # Simpler: set_number - 1 gives completed sets, but we need who won each
    # Use heuristic: if hsg > asg, home won that set
    pts["sets_home"] = (pts["hsg"] > pts["asg"]).astype(int)
    pts["sets_away"] = (pts["asg"] > pts["hsg"]).astype(int)

    # Compute markov fair value for home player
    pts["hp_num"] = pts["home_points"].apply(point_to_num)
    pts["ap_num"] = pts["away_points"].apply(point_to_num)
    pts["markov_fv"] = pts.apply(
        lambda r: markov_fair_value(
            r["sets_home"], r["sets_away"],
            r["home_games"], r["away_games"],
            r["hp_num"], r["ap_num"],
            r["server"], r["is_tiebreak"] == 1,
        ),
        axis=1,
    )

    # Load market tickers per event
    mk_q = "SELECT event_ticker, market_ticker FROM markets ORDER BY event_ticker, market_ticker"
    mk = pd.read_sql_query(mk_q, conn)
    # first market = home, second = away (matches backtest ordering)
    mk["idx"] = mk.groupby("event_ticker").cumcount()
    home_mk = mk[mk["idx"] == 0][["event_ticker", "market_ticker"]].rename(
        columns={"market_ticker": "home_mkt"}
    )
    pts = pts.merge(home_mk, left_on="match_ticker", right_on="event_ticker", how="left")

    # Load tick prices for home markets
    # For each point, find price at point_ts and price 60s forward
    print("Loading tick prices...")
    tick_q = """
    SELECT market_ticker, ts, price
    FROM ticks
    WHERE price IS NOT NULL AND price > 0
    ORDER BY market_ticker, ts
    """
    ticks = pd.read_sql_query(tick_q, conn)
    print(f"  {len(ticks)} ticks loaded")

    # For each point, find nearest tick price at point_ts and at +60s
    # Group ticks by market for fast lookup
    results = []
    for mkt, mkt_ticks in ticks.groupby("market_ticker"):
        pts_m = pts[pts["home_mkt"] == mkt]
        if pts_m.empty:
            continue
        ts_arr = mkt_ticks["ts"].values
        price_arr = mkt_ticks["price"].values
        for _, row in pts_m.iterrows():
            t = row["ts_ms"]
            t_fwd = t + FORWARD_WINDOW_MS
            # price at point
            idx = np.searchsorted(ts_arr, t, side="right") - 1
            if idx < 0 or idx >= len(ts_arr):
                continue
            price_now = price_arr[idx]
            # price 60s forward
            idx_fwd = np.searchsorted(ts_arr, t_fwd, side="right") - 1
            if idx_fwd < 0 or idx_fwd >= len(ts_arr):
                continue
            price_fwd = price_arr[idx_fwd]
            if abs(ts_arr[idx] - t) > 10_000:  # no price within 10s
                continue
            if abs(ts_arr[idx_fwd] - t_fwd) > 10_000:
                continue
            results.append({
                "match_ticker": row["match_ticker"],
                "ts_ms": t,
                "price_now": price_now,
                "price_fwd": price_fwd,
                "markov_fv": row["markov_fv"],
                "series_id": SERIES_MAP.get(row["series_ticker"], -1),
                "server": row["server"],
                "home_games": row["home_games"],
                "away_games": row["away_games"],
                "hp_num": row["hp_num"],
                "ap_num": row["ap_num"],
                "sets_home": row["sets_home"],
                "sets_away": row["sets_away"],
                "is_bp": row["is_break_point"],
                "is_tb": row["is_tiebreak"],
            })

    df = pd.DataFrame(results)
    return df


def featurize(df):
    f = pd.DataFrame()
    f["price_now"] = df["price_now"]
    f["markov_fv"] = df["markov_fv"]
    f["residual"] = df["markov_fv"] - df["price_now"]
    f["series_id"] = df["series_id"]
    f["server"] = df["server"]
    f["home_games"] = df["home_games"]
    f["away_games"] = df["away_games"]
    f["hp_num"] = df["hp_num"]
    f["ap_num"] = df["ap_num"]
    f["point_diff"] = df["hp_num"] - df["ap_num"]
    f["game_diff"] = df["home_games"] - df["away_games"]
    f["sets_home"] = df["sets_home"]
    f["sets_away"] = df["sets_away"]
    f["is_bp"] = df["is_bp"]
    f["is_tb"] = df["is_tb"]
    return f


def main():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    df = load_data(conn)
    conn.close()

    if df.empty:
        print("No data with price overlap. Need more gold-set matches.")
        return

    print(f"\nLoaded {len(df)} point-price pairs from {df['match_ticker'].nunique()} matches")

    # Target: forward residual = markov_fv - price_fwd
    # If positive, market will rise (undervalued). If negative, fall.
    df["target"] = df["markov_fv"] - df["price_fwd"]
    df["current_residual"] = df["markov_fv"] - df["price_now"]

    print(f"Target (markov_fv - price_60s_fwd): mean={df['target'].mean():.4f} std={df['target'].std():.4f}")
    print(f"Current residual (markov_fv - price_now): mean={df['current_residual'].mean():.4f} std={df['current_residual'].std():.4f}")

    # Also predict raw forward price move
    df["fwd_move"] = df["price_fwd"] - df["price_now"]
    print(f"Forward 60s price move: mean={df['fwd_move'].mean():.4f} std={df['fwd_move'].std():.4f}")

    X = featurize(df)
    # Predict raw forward price move, not residual (residual persists, doesn't revert)
    y = df["fwd_move"].values
    groups = df["match_ticker"].values

    # Group split: 80% train, 20% val by match
    unique_matches = df["match_ticker"].unique()
    n_val = max(1, len(unique_matches) // 5)
    val_matches = unique_matches[:n_val]
    val_mask = df["match_ticker"].isin(val_matches)
    tr_mask = ~val_mask

    X_tr, X_va = X[tr_mask], X[val_mask]
    y_tr, y_va = y[tr_mask], y[val_mask]

    print(f"\nTrain: {len(X_tr)} | Val: {len(X_va)} ({n_val} matches)")

    model = lgb.LGBMRegressor(
        n_estimators=200,
        max_depth=6,
        learning_rate=0.05,
        num_leaves=31,
        min_child_samples=20,
        subsample=0.8,
        colsample_bytree=0.8,
        reg_alpha=0.1,
        reg_lambda=0.1,
        verbose=-1,
    )
    model.fit(X_tr, y_tr, eval_set=[(X_va, y_va)], callbacks=[lgb.early_stopping(20)])

    pred = model.predict(X_va)
    rmse = np.sqrt(mean_squared_error(y_va, pred))
    r2 = r2_score(y_va, pred)
    print(f"\nVal RMSE: {rmse:.4f}")
    print(f"Val R²: {r2:.4f}")

    # Feature importance
    imp = pd.DataFrame({
        "feature": X.columns,
        "importance": model.feature_importances_,
    }).sort_values("importance", ascending=False)
    print("\nFeature importance:")
    print(imp.to_string(index=False))

    # Trade simulation: if predicted forward move > threshold, buy
    print("\n--- Trade simulation (val set) ---")
    for thresh in [0.01, 0.02, 0.03, 0.05]:
        buy_mask = pred > thresh
        if buy_mask.sum() == 0:
            continue
        # if we buy at price_now, payout is price_fwd
        # PnL = (price_fwd - price_now) * size
        pnl = df[val_mask]["fwd_move"].values[buy_mask]
        n = buy_mask.sum()
        hit = (pnl > 0).sum()
        print(f"  buy thresh={thresh:.2f}: n={n}, hit={hit/n:.2%}, avg_pnl={pnl.mean():.4f}, total={pnl.sum():.2f}")
    for thresh in [-0.01, -0.02, -0.03, -0.05]:
        sell_mask = pred < thresh
        if sell_mask.sum() == 0:
            continue
        # if we sell at price_now, buy back at price_fwd
        # PnL = (price_now - price_fwd) * size
        pnl = -df[val_mask]["fwd_move"].values[sell_mask]
        n = sell_mask.sum()
        hit = (pnl > 0).sum()
        print(f"  sell thresh={thresh:.2f}: n={n}, hit={hit/n:.2%}, avg_pnl={pnl.mean():.4f}, total={pnl.sum():.2f}")

    # Export model
    model_json = model.booster_.dump_model()
    out = {
        "model_type": "lightgbm_regression",
        "version": 1,
        "trained_at": pd.Timestamp.now().isoformat(),
        "n_samples": len(df),
        "n_matches": int(df["match_ticker"].nunique()),
        "val_rmse": float(rmse),
        "val_r2": float(r2),
        "feature_names": list(X.columns),
        "target": "price_60s_forward - price_now",
        "forward_window_ms": FORWARD_WINDOW_MS,
        "lgbm_model": model_json,
    }
    out_path = OUT_DIR / "markov_residual_lgbm.json"
    out_path.write_text(json.dumps(out, indent=2))
    print(f"\nExported to {out_path}")


if __name__ == "__main__":
    main()
