#!/usr/bin/env python3
"""Main pipeline runner.

1. Load extracted data
2. Train HMM on all matches (pooled)
3. Build aligned feature datasets
4. Train + evaluate models (baseline, momentum_only, combined)
5. SHAP feature importance on best model
6. Sobol sensitivity analysis
7. Backtest simulated trades
8. Save all results
"""

import json
import sys
from pathlib import Path

import numpy as np
import pandas as pd

from data import load_config, load_all_matches
from features.court_features import build_observations_multi, train_hmm
from align import build_all_datasets
from models import run_all_experiments, train_final_model
from evaluate import (
    print_results_table,
    compute_shap,
    sobol_sensitivity,
    plot_roc_curves,
    plot_confusion_matrix,
)
from backtest import run_backtest_comparison
from features.market_features import compute_price_target
from data import get_home_away_market_tickers


OUT_DIR = Path("results")


def main():
    config = load_config("config.yaml")
    OUT_DIR.mkdir(exist_ok=True)

    # 1. Load data
    print("Loading extracted data...")
    matches = load_all_matches(config["data"]["extracted_dir"])
    print(f"  {len(matches)} matches loaded")

    # 2. Train HMM (pooled across all matches)
    print("\nTraining HMM (pooled)...")
    all_points = [p for p, t, m, meta in matches if not p.empty]
    X_hmm, lengths = build_observations_multi(all_points)
    print(f"  HMM observations: {X_hmm.shape[0]} points, {len(lengths)} sequences")

    hmm_model = train_hmm(
        X_hmm, lengths,
        n_states=config["hmm"]["n_states"],
        n_iter=config["hmm"]["n_iter"],
        random_state=config["hmm"]["random_state"],
    )
    print(f"  HMM trained: {hmm_model.n_components} states")
    print(f"  State means (server_won): {hmm_model.means_[:, 1]}")

    # 3. Build datasets
    print("\nBuilding feature datasets...")
    X, y, labels, prices_entry, prices_exit = build_all_datasets(
        matches, hmm_model=hmm_model, config=config
    )

    if X is None:
        print("ERROR: no valid data. Exiting.")
        sys.exit(1)

    print(f"\nTotal samples: {len(X)}")
    print(f"Positive rate (price up): {y.mean():.3f}")
    print(f"Matches: {labels.nunique()}")
    print(f"Features: {list(X.columns)}")

    # 4. Run experiments
    print("\nRunning experiments (leave-one-match-out CV)...")
    results = run_all_experiments(X, y, labels, config)
    print_results_table(results)
    results.to_csv(OUT_DIR / "model_comparison.csv", index=False)

    # 5. SHAP on combined model
    print("\nComputing SHAP values (combined + xgboost)...")
    model, X_sel, feature_cols = train_final_model(
        X, y, "combined", "xgboost", config
    )
    shap_df = compute_shap(
        model, X_sel, feature_cols,
        OUT_DIR / "shap_combined_xgboost.png",
        model_type="xgboost",
    )
    print("\nSHAP feature importance:")
    print(shap_df.to_string(index=False))
    shap_df.to_csv(OUT_DIR / "shap_importance.csv", index=False)

    # 6. Sobol sensitivity
    print("\nSobol sensitivity analysis...")
    sobol_df = sobol_sensitivity(
        model, X_sel, feature_cols, n_samples=2000, model_type="xgboost"
    )
    print(sobol_df.to_string(index=False))
    sobol_df.to_csv(OUT_DIR / "sobol_sensitivity.csv", index=False)

    # 7. Backtest
    print("\nRunning backtest...")

    bt_results = run_backtest_comparison(
        X, y, labels, prices_entry, prices_exit, config
    )
    print("\nBacktest results:")
    cols = ["variant", "n_trades", "hit_rate", "return_pct", "sharpe_per_trade",
            "profit_factor", "max_drawdown"]
    print(bt_results[cols].to_string(index=False))
    bt_results.to_csv(OUT_DIR / "backtest_results.csv", index=False)

    # 8. Save summary
    summary = {
        "total_samples": len(X),
        "positive_rate": float(y.mean()),
        "n_matches": int(labels.nunique()),
        "hmm_states": config["hmm"]["n_states"],
        "horizon_sec": config["target"]["horizon_sec"][1],
        "best_model_auc": float(results["auc"].max()),
        "best_model_variant": results.loc[results["auc"].idxmax(), "variant"],
        "best_model_type": results.loc[results["auc"].idxmax(), "model_type"],
    }
    with open(OUT_DIR / "summary.json", "w") as f:
        json.dump(summary, f, indent=2)

    print(f"\nResults saved to {OUT_DIR}/")
    print(f"Best model: {summary['best_model_variant']} + {summary['best_model_type']} "
          f"(AUC={summary['best_model_auc']:.3f})")


if __name__ == "__main__":
    main()
