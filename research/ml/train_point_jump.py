#!/usr/bin/env python3
"""Train point-jump predictor (M2 revised).

LightGBM: predict price jump at point events.
pp-latency showed 17s median lag point→price move.
If we predict jump direction, we have edge window.

Target: price_5s_after_point - price_5s_before_point
Features: who scored, server, expected (server vs returner win),
          score state, current price, series, markov_fv
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

# Markov fair value (simplified — same as train_markov_residual.py)
def markov_game_win(p_serve, hp, ap, server_is_home, is_tiebreak):
    if is_tiebreak:
        return 0.5
    if hp >= 4 and hp - ap >= 2:
        return 1.0
    if ap >= 4 and ap - hp >= 2:
        return 0.0
    if hp == ap and hp >= 3:
        p = p_serve if server_is_home else (1 - p_serve)
        return p * p / (1 - 2 * p * (1 - p))
    if hp >= 4 and ap < 4:
        p = p_serve if server_is_home else (1 - p_serve)
        return p + (1 - p) * markov_game_win(p_serve, 3, 3, server_is_home, False)
    if ap >= 4 and hp < 4:
        p = p_serve if server_is_home else (1 - p_serve)
        return (1 - p) * markov_game_win(p_serve, 3, 3, server_is_home, False)
    p = p_serve if server_is_home else (1 - p_serve)
    return p * markov_game_win(p_serve, hp + 1, ap, server_is_home, False) + \
           (1 - p) * markov_game_win(p_serve, hp, ap + 1, server_is_home, False)


def markov_set_win(p_serve, gh, ga, p_home_game):
    if gh >= 6 and gh - ga >= 2:
        return 1.0
    if ga >= 6 and ga - gh >= 2:
        return 0.0
    if gh == 6 and ga == 6:
        return 0.5
    if gh >= 7:
        return 1.0
    if ga >= 7:
        return 0.0
    return p_home_game * markov_set_win(p_serve, gh + 1, ga, 1 - p_home_game) + \
           (1 - p_home_game) * markov_set_win(p_serve, gh, ga + 1, 1 - p_home_game)


def markov_match_win(sh, sa, p_hs):
    if sh >= 2:
        return 1.0
    if sa >= 2:
        return 0.0
    return p_hs * markov_match_win(sh + 1, sa, 1 - p_hs) + \
           (1 - p_hs) * markov_match_win(sh, sa + 1, 1 - p_hs)


def markov_fv(sh, sa, gh, ga, hp, ap, server, is_tb, p_serve=0.64):
    pg = markov_game_win(p_serve, hp, ap, server == 1, is_tb)
    ps = markov_set_win(p_serve, gh, ga, pg)
    return markov_match_win(sh, sa, ps)


def main():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)

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
    pts["hp_num"] = pts["home_points"].map(POINT_MAP).fillna(4).astype(int)
    pts["ap_num"] = pts["away_points"].map(POINT_MAP).fillna(4).astype(int)
    pts["sets_home"] = (pts["hsg"] > pts["asg"]).astype(int)
    pts["sets_away"] = (pts["asg"] > pts["hsg"]).astype(int)
    pts["series_id"] = pts["series_ticker"].map(SERIES_MAP).fillna(-1).astype(int)
    pts["server_won"] = (pts["scorer"] == pts["server"]).astype(int)
    pts["home_scored"] = (pts["scorer"] == 1).astype(int)

    # Compute markov FV BEFORE this point (state before point was scored)
    pts["markov_fv_before"] = pts.apply(
        lambda r: markov_fv(
            r["sets_home"], r["sets_away"],
            r["home_games"], r["away_games"],
            r["hp_num"], r["ap_num"],
            r["server"], r["is_tiebreak"] == 1,
        ),
        axis=1,
    )

    # Load home market tickers
    mk_q = "SELECT event_ticker, market_ticker FROM markets ORDER BY event_ticker, market_ticker"
    mk = pd.read_sql_query(mk_q, conn)
    mk["idx"] = mk.groupby("event_ticker").cumcount()
    home_mk = mk[mk["idx"] == 0][["event_ticker", "market_ticker"]].rename(
        columns={"market_ticker": "home_mkt"}
    )
    pts = pts.merge(home_mk, left_on="match_ticker", right_on="event_ticker", how="left")

    # Load ticks
    print("Loading ticks...")
    tick_q = """
    SELECT market_ticker, ts, price
    FROM ticks WHERE price IS NOT NULL AND price > 0
    ORDER BY market_ticker, ts
    """
    ticks = pd.read_sql_query(tick_q, conn)
    conn.close()
    print(f"  {len(ticks)} ticks")

    # For each point, find price 5s before and 5s after
    results = []
    for mkt, mkt_ticks in ticks.groupby("market_ticker"):
        pts_m = pts[pts["home_mkt"] == mkt]
        if pts_m.empty:
            continue
        ts_arr = mkt_ticks["ts"].values
        price_arr = mkt_ticks["price"].values
        for _, row in pts_m.iterrows():
            t = row["ts_ms"]
            # price 5s before
            idx_before = np.searchsorted(ts_arr, t - 5000, side="right") - 1
            if idx_before < 0 or idx_before >= len(ts_arr):
                continue
            price_before = price_arr[idx_before]
            if abs(ts_arr[idx_before] - (t - 5000)) > 5000:
                continue
            # price 5s after
            idx_after = np.searchsorted(ts_arr, t + 5000, side="right") - 1
            if idx_after < 0 or idx_after >= len(ts_arr):
                continue
            price_after = price_arr[idx_after]
            if abs(ts_arr[idx_after] - (t + 5000)) > 5000:
                continue
            jump = price_after - price_before
            results.append({
                "match_ticker": row["match_ticker"],
                "price_before": price_before,
                "price_after": price_after,
                "jump": jump,
                "markov_fv": row["markov_fv_before"],
                "series_id": row["series_id"],
                "server": row["server"],
                "scorer": row["scorer"],
                "server_won": row["server_won"],
                "home_scored": row["home_scored"],
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
    if df.empty:
        print("No data with price overlap.")
        return

    print(f"\n{len(df)} point-jump pairs from {df['match_ticker'].nunique()} matches")
    print(f"Jump: mean={df['jump'].mean():.4f} std={df['jump'].std():.4f}")
    print(f"  home scored: jump={df[df['home_scored']==1]['jump'].mean():.4f}")
    print(f"  away scored: jump={df[df['home_scored']==0]['jump'].mean():.4f}")
    print(f"  server won:  jump={df[df['server_won']==1]['jump'].mean():.4f}")
    print(f"  server lost: jump={df[df['server_won']==0]['jump'].mean():.4f}")

    # Features
    f = pd.DataFrame()
    f["price_before"] = df["price_before"]
    f["markov_fv"] = df["markov_fv"]
    f["residual"] = df["markov_fv"] - df["price_before"]
    f["series_id"] = df["series_id"]
    f["server"] = df["server"]
    f["scorer"] = df["scorer"]
    f["server_won"] = df["server_won"]
    f["home_scored"] = df["home_scored"]
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

    X = f.values
    y = df["jump"].values

    # Group split
    um = df["match_ticker"].unique()
    n_val = max(1, len(um) // 5)
    val_m = um[:n_val]
    val_mask = df["match_ticker"].isin(val_m).values
    tr_mask = ~val_mask

    X_tr, X_va = X[tr_mask], X[val_mask]
    y_tr, y_va = y[tr_mask], y[val_mask]
    print(f"\nTrain: {len(X_tr)} | Val: {len(X_va)}")

    model = lgb.LGBMRegressor(
        n_estimators=300, max_depth=6, learning_rate=0.05,
        num_leaves=31, min_child_samples=20,
        subsample=0.8, colsample_bytree=0.8,
        reg_alpha=0.1, reg_lambda=0.1, verbose=-1,
    )
    model.fit(X_tr, y_tr, eval_set=[(X_va, y_va)], callbacks=[lgb.early_stopping(30)])

    pred = model.predict(X_va)
    rmse = np.sqrt(mean_squared_error(y_va, pred))
    r2 = r2_score(y_va, pred)
    print(f"\nVal RMSE: {rmse:.4f}")
    print(f"Val R²: {r2:.4f}")

    imp = pd.DataFrame({
        "feature": f.columns,
        "importance": model.feature_importances_,
    }).sort_values("importance", ascending=False)
    print("\nFeature importance:")
    print(imp.to_string(index=False))

    # Trade sim: buy if predicted jump > threshold
    print("\n--- Trade simulation (val set, 5s horizon) ---")
    for thresh in [0.01, 0.02, 0.03, 0.05]:
        buy_mask = pred > thresh
        if buy_mask.sum() == 0:
            continue
        pnl = df[val_mask]["jump"].values[buy_mask]
        n = buy_mask.sum()
        hit = (pnl > 0).sum()
        print(f"  buy>{thresh:.2f}: n={n}, hit={hit/n:.2%}, avg={pnl.mean():.4f}, total={pnl.sum():.2f}")
    for thresh in [-0.01, -0.02, -0.03, -0.05]:
        sell_mask = pred < thresh
        if sell_mask.sum() == 0:
            continue
        pnl = -df[val_mask]["jump"].values[sell_mask]
        n = sell_mask.sum()
        hit = (pnl > 0).sum()
        print(f"  sell<{thresh:.2f}: n={n}, hit={hit/n:.2%}, avg={pnl.mean():.4f}, total={pnl.sum():.2f}")

    # Export
    model_json = model.booster_.dump_model()
    out = {
        "model_type": "lightgbm_regression",
        "version": 2,
        "trained_at": pd.Timestamp.now().isoformat(),
        "n_samples": len(df),
        "n_matches": int(df["match_ticker"].nunique()),
        "val_rmse": float(rmse),
        "val_r2": float(r2),
        "feature_names": list(f.columns),
        "target": "price_5s_after_point - price_5s_before_point",
        "horizon_s": 5,
        "lgbm_model": model_json,
    }
    out_path = OUT_DIR / "point_jump_lgbm.json"
    out_path.write_text(json.dumps(out, indent=2))
    print(f"\nExported to {out_path}")


if __name__ == "__main__":
    main()
