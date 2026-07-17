#!/usr/bin/env python3
"""Train serve-win probability model (M1).

Logistic regression: P(server wins point | series, score_state, is_break_point).
Replaces global pServe=0.64 with per-context estimate.

Exports weights to JSON for Go runtime loading.
"""
import json
import sqlite3
import sys
from pathlib import Path

import numpy as np
import pandas as pd
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import accuracy_score, brier_score_loss, log_loss
from sklearn.model_selection import GroupKFold

DB = Path(__file__).resolve().parents[2] / "kalshi_tennis.db"
OUT = Path(__file__).resolve().parent / "models" / "serve_win_logistic.json"

POINT_MAP = {"0": 0, "15": 1, "30": 2, "40": 3, "A": 4}

SERIES_MAP = {
    "KXATPMATCH": 0,
    "KXATPCHALLENGERMATCH": 1,
    "KXITFMATCH": 2,
    "KXITFWMATCH": 3,
    "KXWTAMATCH": 4,
    "KXWTACHALLENGERMATCH": 5,
}


def load_points(conn):
    q = """
    SELECT p.match_ticker, p.server, p.scorer,
           p.home_points, p.away_points,
           p.home_games, p.away_games,
           p.is_tiebreak, p.is_break_point,
           e.series_ticker
    FROM points p
    JOIN events e ON p.match_ticker = e.event_ticker
    WHERE p.ts_ms IS NOT NULL
    """
    df = pd.read_sql_query(q, conn)
    df["server_won"] = (df["scorer"] == df["server"]).astype(int)
    df["is_bp"] = df["is_break_point"].astype(int)
    df["is_tb"] = df["is_tiebreak"].astype(int)
    df["hp"] = df["home_points"].map(POINT_MAP).fillna(4).astype(int)
    df["ap"] = df["away_points"].map(POINT_MAP).fillna(4).astype(int)
    df["series_id"] = df["series_ticker"].map(SERIES_MAP).fillna(-1).astype(int)
    return df


def featurize(df):
    f = pd.DataFrame()
    f["series_id"] = df["series_id"]
    f["server"] = df["server"]
    f["home_games"] = df["home_games"]
    f["away_games"] = df["away_games"]
    f["point_diff"] = df["hp"] - df["ap"]
    f["game_diff"] = df["home_games"] - df["away_games"]
    f["is_bp"] = df["is_bp"]
    f["is_tb"] = df["is_tb"]
    f["server_is_home"] = (df["server"] == 1).astype(int)
    f["hp"] = df["hp"]
    f["ap"] = df["ap"]
    return f.values


def main():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    df = load_points(conn)
    conn.close()
    print(f"Loaded {len(df)} points from {df['match_ticker'].nunique()} matches")
    print(f"Overall server win rate: {df['server_won'].mean():.3f}")

    X = featurize(df)
    y = df["server_won"].values
    groups = df["match_ticker"].values

    # Group K-fold (no match leakage)
    gkf = GroupKFold(n_splits=5)
    accs, briers, logs = [], [], []
    for fold, (tr, va) in enumerate(gkf.split(X, y, groups)):
        m = LogisticRegression(max_iter=1000, C=1.0)
        m.fit(X[tr], y[tr])
        p = m.predict_proba(X[va])[:, 1]
        accs.append(accuracy_score(y[va], (p > 0.5).astype(int)))
        briers.append(brier_score_loss(y[va], p))
        logs.append(log_loss(y[va], p))
        print(f"  fold {fold}: acc={accs[-1]:.3f} brier={briers[-1]:.4f} logloss={logs[-1]:.4f}")

    print(f"\nCV: acc={np.mean(accs):.3f} brier={np.mean(briers):.4f} logloss={np.mean(logs):.4f}")

    # Train on all data for production weights
    full = LogisticRegression(max_iter=1000, C=1.0)
    full.fit(X, y)

    feature_names = [
        "series_id", "server", "home_games", "away_games",
        "point_diff", "game_diff", "is_bp", "is_tb",
        "server_is_home", "hp", "ap",
    ]

    # Per-series serve win rate (for Go fallback)
    series_rates = df.groupby("series_ticker")["server_won"].mean().to_dict()

    # Per-context breakdown for sanity
    print("\nPer-series serve win rate:")
    for s, r in sorted(series_rates.items()):
        n = (df["series_ticker"] == s).sum()
        print(f"  {s}: {r:.3f} (n={n})")

    print("\nBreak point serve win rate:")
    bp = df[df["is_bp"] == 1]
    print(f"  BP: {bp['server_won'].mean():.3f} (n={len(bp)})")
    non_bp = df[df["is_bp"] == 0]
    print(f"  non-BP: {non_bp['server_won'].mean():.3f} (n={len(non_bp)})")

    # Export
    OUT.parent.mkdir(parents=True, exist_ok=True)
    export = {
        "model_type": "logistic_regression",
        "version": 1,
        "trained_at": pd.Timestamp.now().isoformat(),
        "n_samples": len(df),
        "n_matches": int(df["match_ticker"].nunique()),
        "cv_accuracy": float(np.mean(accs)),
        "cv_brier": float(np.mean(briers)),
        "cv_logloss": float(np.mean(logs)),
        "feature_names": feature_names,
        "feature_means": full.coef_.shape,
        "coef": full.coef_[0].tolist(),
        "intercept": float(full.intercept_[0]),
        "series_map": SERIES_MAP,
        "series_rates": {k: float(v) for k, v in series_rates.items()},
        "point_map": POINT_MAP,
        "overall_rate": float(df["server_won"].mean()),
    }
    OUT.write_text(json.dumps(export, indent=2))
    print(f"\nExported to {OUT}")
    print(f"  coef shape: {full.coef_.shape}")
    print(f"  intercept: {full.intercept_[0]:.4f}")


if __name__ == "__main__":
    main()
