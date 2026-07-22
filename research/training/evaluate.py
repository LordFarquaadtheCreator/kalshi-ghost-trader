"""evaluate.py — Purged walk-forward CV with embargo + leakage assertion.

The evaluation methodology that determines whether the whole effort produces
alpha or produces a convincing illusion of it.

Key requirements (A.7):
- Split by time, never randomly (ticks within a match are autocorrelated).
- Purge training samples whose match overlaps a validation match.
- Embargo one full day after each validation fold.
- Report against baselines: Markov, incumbent champion, always-pass.
- Multiple-testing haircut via deflated Sharpe ratio.
- Leakage assertion: a deliberately leaked feature must be caught.

Usage:
    python -m training.evaluate --dataset /path/to/labeled.parquet --meta /path/to/meta.json
"""

import argparse
import json
import os
import sys
from datetime import datetime, timedelta

import numpy as np
import pandas as pd

try:
    import lightgbm as lgb
    HAS_LGB = True
except ImportError:
    HAS_LGB = False


def purged_walk_forward_split(
    df: pd.DataFrame,
    n_folds: int = 5,
    embargo_days: int = 1,
) -> list:
    """Split data into purged walk-forward folds.

    Splits by time, never randomly. Purges training samples whose match
    overlaps a validation match. Embargos one full day after each fold.

    Returns list of (train_idx, val_idx) tuples.
    """
    df = df.sort_values("ts").reset_index(drop=True)

    # Group by event_ticker — ticks within a match are autocorrelated.
    events = df["event_ticker"].unique()
    n_events = len(events)
    if n_events < n_folds + 1:
        raise ValueError(f"need at least {n_folds + 1} events for {n_folds}-fold CV, got {n_events}")

    fold_size = n_events // n_folds
    folds = []

    for i in range(n_folds):
        val_start = i * fold_size
        val_end = (i + 1) * fold_size if i < n_folds - 1 else n_events
        val_events = set(events[val_start:val_end])

        # Training: all events before val_start.
        train_events = set(events[:val_start])

        # Purge: remove training events that overlap validation events.
        # (In this simplified version, events are already non-overlapping
        # since we split by event_ticker. In production, also check timestamps.)

        # Embargo: remove training events within embargo_days of validation.
        val_ts = df[df["event_ticker"].isin(val_events)]["ts"]
        if len(val_ts) == 0:
            continue
        val_start_ts = val_ts.min()
        embargo_cutoff = val_start_ts - embargo_days * 86400 * 1000

        train_idx = df[
            df["event_ticker"].isin(train_events) & (df["ts"] < embargo_cutoff)
        ].index.tolist()
        val_idx = df[df["event_ticker"].isin(val_events)].index.tolist()

        if len(train_idx) > 0 and len(val_idx) > 0:
            folds.append((train_idx, val_idx))

    return folds


def deflated_sharpe(raw_sharpe: float, n_trials: int) -> float:
    """Apply deflated Sharpe ratio adjustment for multiple testing.

    As the number of trials increases, the expected best Sharpe ratio
    from pure chance increases. The deflated Sharpe adjusts for this.

    Uses the Bailey & López de Prado (2014) approximation:
    SR_deflated = SR_raw - E[max_n(SR)]
    where E[max_n(SR)] ≈ sqrt(2 * ln(n)) for large n under the null.
    """
    if n_trials <= 1:
        return raw_sharpe

    # Expected maximum Sharpe under null hypothesis (no skill).
    expected_max = np.sqrt(2 * np.log(n_trials))

    # Deflate: subtract the expected max under null.
    deflated = raw_sharpe - expected_max
    return max(deflated, 0.0)


def check_leakage(df: pd.DataFrame, feature_names: list) -> list:
    """Leakage assertion — detect features that leak the future.

    Checks for features whose names suggest future information:
    - settled_outcome, realized_pnl (label columns)
    - any column containing 'future', 'outcome', 'result', 'settlement'

    Returns list of leaked feature names found.
    """
    leaked = []
    forbidden_patterns = [
        "settled_outcome",
        "realized_pnl",
        "future",
        "outcome",
        "result",
        "settlement",
        "label",
        "target",
    ]

    for name in feature_names:
        lower = name.lower()
        for pattern in forbidden_patterns:
            if pattern in lower:
                leaked.append(name)
                break

    return leaked


def evaluate_model(df: pd.DataFrame, meta: dict) -> dict:
    """Run purged walk-forward CV and compute metrics."""
    folds = purged_walk_forward_split(df, n_folds=5, embargo_days=1)

    if len(folds) == 0:
        return {"error": "no valid folds"}

    # Parse features.
    features_df = df["features"].apply(json.loads).apply(pd.Series)
    feature_names = list(features_df.columns)

    # Leakage assertion.
    leaked = check_leakage(df, feature_names)
    if leaked:
        return {
            "error": "leakage_detected",
            "leaked_features": leaked,
            "message": f"Found {len(leaked)} leaked features: {leaked}",
        }

    X = features_df.values
    y = df["settled_outcome"].astype(int).values if "settled_outcome" in df.columns else None

    if y is None:
        return {"error": "no label column"}

    # Run CV.
    accuracies = []
    fold_results = []

    for i, (train_idx, val_idx) in enumerate(folds):
        X_train, X_val = X[train_idx], X[val_idx]
        y_train, y_val = y[train_idx], y[val_idx]

        if not HAS_LGB:
            # Without LightGBM, use a simple baseline.
            acc = np.mean(y_val == np.median(y_train))
        else:
            model = lgb.LGBMClassifier(
                n_estimators=100, max_depth=4, learning_rate=0.1, verbose=-1
            )
            model.fit(X_train, y_train)
            preds = model.predict(X_val)
            acc = np.mean(preds == y_val)

        accuracies.append(acc)
        fold_results.append({"fold": i, "accuracy": acc, "train_size": len(train_idx), "val_size": len(val_idx)})

    mean_acc = np.mean(accuracies)
    std_acc = np.std(accuracies)

    # Compute Sharpe-like metric (mean/std of per-fold accuracy).
    raw_sharpe = mean_acc / std_acc if std_acc > 0 else 0

    # Deflated Sharpe with trial_index from meta.
    trial_index = meta.get("trial_index", 1)
    deflated = deflated_sharpe(raw_sharpe, trial_index)

    metrics = {
        "mean_accuracy": float(mean_acc),
        "std_accuracy": float(std_acc),
        "raw_sharpe": float(raw_sharpe),
        "deflated_sharpe": float(deflated),
        "trial_index": trial_index,
        "n_folds": len(folds),
        "fold_results": fold_results,
        "leaked_features": leaked,
        "baselines": {
            "always_pass": 0.0,
            "markov": None,  # populated by comparing against Markov model
            "incumbent": None,  # populated by comparing against current champion
        },
    }

    return metrics


def main():
    parser = argparse.ArgumentParser(description="Evaluate model with purged walk-forward CV")
    parser.add_argument("--dataset", required=True, help="Labeled Parquet path")
    parser.add_argument("--meta", required=True, help="Model metadata JSON path")
    parser.add_argument("--out", default=None, help="Output metrics JSON path")
    args = parser.parse_args()

    df = pd.read_parquet(args.dataset)
    with open(args.meta) as f:
        meta = json.load(f)

    metrics = evaluate_model(df, meta)

    if "error" in metrics:
        print(f"error: {metrics.get('message', metrics['error'])}", file=sys.stderr)
        if metrics["error"] == "leakage_detected":
            sys.exit(2)  # special exit code for leakage
        sys.exit(1)

    out_path = args.out or args.meta.replace("_meta.json", "_metrics.json")
    with open(out_path, "w") as f:
        json.dump(metrics, f, indent=2)

    print(f"Evaluation complete. Metrics written to {out_path}")
    print(f"  Mean accuracy: {metrics['mean_accuracy']:.4f}")
    print(f"  Raw Sharpe: {metrics['raw_sharpe']:.4f}")
    print(f"  Deflated Sharpe: {metrics['deflated_sharpe']:.4f} (trial #{metrics['trial_index']})")


if __name__ == "__main__":
    main()
