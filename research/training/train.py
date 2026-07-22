"""train.py — Train a model with rolling window + exponential sample weighting.

Stage 1: LightGBM supervised fair value model.
Stage 2: LightGBM contextual bandit value model.
Stage 3: Small MLP sequential policy (separate trainer).

Rolling window: rl_train_window_days (default 180).
Exponential sample weighting: half-life rl_sample_halflife_days (default 45).

Usage:
    python -m training.train --family fairvalue --dataset /path/to/labeled.parquet
"""

import argparse
import json
import os
import sys
import time
from datetime import datetime, timedelta

import numpy as np
import pandas as pd

try:
    import lightgbm as lgb
    HAS_LGB = True
except ImportError:
    HAS_LGB = False


def compute_sample_weights(ts: pd.Series, halflife_days: int) -> np.ndarray:
    """Exponential sample weighting with given half-life.

    More recent samples get higher weight to handle non-stationarity.
    """
    max_ts = ts.max()
    age_days = (max_ts - ts) / (1000 * 86400)  # ts is in ms
    weights = np.power(0.5, age_days / halflife_days)
    return weights.values


def train_fairvalue(df: pd.DataFrame, train_window_days: int, halflife_days: int):
    """Train a Stage 1 fair value model.

    Predicts P(home wins | match state, market state).
    Label: settled_outcome (1=home win, 0=away win).
    """
    if not HAS_LGB:
        raise ImportError("lightgbm not installed. Run: pip install lightgbm")

    # Filter to rows with labels.
    labeled = df[df["settled_outcome"].notna()].copy()
    if len(labeled) == 0:
        raise ValueError("no labeled rows for training")

    # Parse features from JSON.
    features = labeled["features"].apply(json.loads).apply(pd.Series)
    feature_names = list(features.columns)

    X = features.values
    y = labeled["settled_outcome"].astype(int).values
    weights = compute_sample_weights(labeled["ts"], halflife_days)

    # Rolling window: only use last train_window_days.
    max_ts = labeled["ts"].max()
    cutoff = max_ts - train_window_days * 86400 * 1000
    mask = labeled["ts"] >= cutoff
    X = X[mask]
    y = y[mask]
    weights = weights[mask]

    print(f"Training on {len(X)} samples, {len(feature_names)} features")

    model = lgb.LGBMClassifier(
        n_estimators=500,
        max_depth=6,
        learning_rate=0.05,
        subsample=0.8,
        colsample_bytree=0.8,
        reg_alpha=0.1,
        reg_lambda=0.1,
        random_state=42,
        verbose=-1,
    )
    model.fit(X, y, sample_weight=weights)

    return model, feature_names


def main():
    parser = argparse.ArgumentParser(description="Train a model")
    parser.add_argument("--family", required=True, choices=["fairvalue", "bandit", "sequential"])
    parser.add_argument("--dataset", required=True, help="Labeled Parquet path")
    parser.add_argument("--out", default="/var/lib/ghost/models", help="Output directory")
    parser.add_argument("--train-window-days", type=int, default=180)
    parser.add_argument("--halflife-days", type=int, default=45)
    args = parser.parse_args()

    df = pd.read_parquet(args.dataset)
    print(f"Loaded {len(df)} rows from {args.dataset}")

    if args.family == "fairvalue":
        model, feature_names = train_fairvalue(df, args.train_window_days, args.halflife_days)
    else:
        raise NotImplementedError(f"family {args.family} not yet implemented")

    # Save artifact.
    os.makedirs(args.out, exist_ok=True)
    version = int(time.time())
    artifact_path = os.path.join(args.out, f"{args.family}_v{version}.txt")
    model.booster_.save_model(artifact_path)
    print(f"Saved artifact to {artifact_path}")

    # Save metadata.
    meta = {
        "family": args.family,
        "version": version,
        "trained_at": int(time.time()),
        "feature_hash": df["feature_hash"].iloc[0] if "feature_hash" in df.columns else "",
        "feature_names": feature_names,
        "artifact_path": artifact_path,
        "train_window_days": args.train_window_days,
        "halflife_days": args.halflife_days,
    }
    meta_path = os.path.join(args.out, f"{args.family}_v{version}_meta.json")
    with open(meta_path, "w") as f:
        json.dump(meta, f, indent=2)
    print(f"Saved metadata to {meta_path}")


if __name__ == "__main__":
    main()
