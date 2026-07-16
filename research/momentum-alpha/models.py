"""Models: baseline, momentum-only, combined.

Three model variants to answer: does on-court momentum add alpha
beyond market microstructure alone?

1. baseline: market features only (no momentum)
2. momentum_only: court features only (no market)
3. combined: court + market features

Uses XGBoost (paper section 4) and LightGBM (paper section 5).
"""

import numpy as np
import pandas as pd
import xgboost as xgb
import lightgbm as lgb
from sklearn.model_selection import GroupKFold
from sklearn.metrics import accuracy_score, roc_auc_score, f1_score, precision_score, recall_score


COURT_FEATURES = [
    "hmm_state", "momentum_raw", "momentum_ema",
    "serve_win_rate_home", "serve_win_rate_away",
    "break_rate_home", "break_rate_away",
    "score_diff_games", "score_diff_sets",
    "points_into_game", "is_break_point", "is_server_home",
]

MARKET_FEATURES = [
    "price", "spread", "bid_ask_imbalance",
    "price_velocity_10s", "price_velocity_30s",
    "volume_velocity_30s", "trade_flow_imbalance",
]


def get_feature_set(X, variant):
    """Select features for model variant."""
    if variant == "baseline":
        cols = [c for c in MARKET_FEATURES if c in X.columns]
    elif variant == "momentum_only":
        cols = [c for c in COURT_FEATURES if c in X.columns]
    elif variant == "combined":
        cols = [c for c in COURT_FEATURES + MARKET_FEATURES if c in X.columns]
    else:
        raise ValueError(f"unknown variant: {variant}")
    return X[cols].copy(), cols


def train_xgboost(X_train, y_train, config):
    """Train XGBoost classifier."""
    params = config["models"]["xgboost"]
    model = xgb.XGBClassifier(
        max_depth=params["max_depth"],
        n_estimators=params["n_estimators"],
        learning_rate=params["learning_rate"],
        subsample=params["subsample"],
        colsample_bytree=params["colsample_bytree"],
        random_state=params["random_state"],
        eval_metric="logloss",
    )
    model.fit(X_train, y_train)
    return model


def train_lightgbm(X_train, y_train, config):
    """Train LightGBM classifier."""
    params = config["models"]["lightgbm"]
    model = lgb.LGBMClassifier(
        max_depth=params["max_depth"],
        n_estimators=params["n_estimators"],
        learning_rate=params["learning_rate"],
        subsample=params["subsample"],
        colsample_bytree=params["colsample_bytree"],
        random_state=params["random_state"],
        verbose=-1,
    )
    model.fit(X_train, y_train)
    return model


def cross_validate(X, y, groups, variant, model_type, config, n_splits=5):
    """Group k-fold CV. Groups = match_ticker (leave-one-match-out).

    Returns dict of metrics averaged across folds.
    """
    X_sel, feature_cols = get_feature_set(X, variant)

    # fill NaN
    X_sel = X_sel.fillna(0)

    n_groups = groups.nunique()
    n_splits = min(n_splits, n_groups)

    fold_metrics = []
    skf = GroupKFold(n_splits=n_splits)

    for fold_idx, (train_idx, test_idx) in enumerate(skf.split(X_sel, y, groups)):
        X_train, X_test = X_sel.iloc[train_idx], X_sel.iloc[test_idx]
        y_train, y_test = y.iloc[train_idx], y.iloc[test_idx]

        if y_train.nunique() < 2 or y_test.nunique() < 2:
            continue

        if model_type == "xgboost":
            model = train_xgboost(X_train, y_train, config)
        else:
            model = train_lightgbm(X_train, y_train, config)

        y_pred = model.predict(X_test)
        y_proba = model.predict_proba(X_test)[:, 1]

        metrics = {
            "accuracy": accuracy_score(y_test, y_pred),
            "auc": roc_auc_score(y_test, y_proba),
            "f1": f1_score(y_test, y_pred, zero_division=0),
            "precision": precision_score(y_test, y_pred, zero_division=0),
            "recall": recall_score(y_test, y_pred, zero_division=0),
            "n_test": len(y_test),
        }
        fold_metrics.append(metrics)

    if not fold_metrics:
        return None

    # average
    avg = {}
    for key in fold_metrics[0]:
        if key == "n_test":
            avg[key] = sum(m[key] for m in fold_metrics)
        else:
            avg[key] = np.mean([m[key] for m in fold_metrics])

    avg["feature_cols"] = feature_cols
    avg["n_folds"] = len(fold_metrics)
    return avg


def run_all_experiments(X, y, groups, config):
    """Run all model variants and return comparison table."""
    results = []

    for variant in ["baseline", "momentum_only", "combined"]:
        for model_type in ["xgboost", "lightgbm"]:
            print(f"  {variant} + {model_type}...")
            metrics = cross_validate(X, y, groups, variant, model_type, config)
            if metrics is None:
                print(f"    skipped (insufficient data)")
                continue
            metrics["variant"] = variant
            metrics["model_type"] = model_type
            results.append(metrics)
            print(f"    acc={metrics['accuracy']:.3f} auc={metrics['auc']:.3f} "
                  f"f1={metrics['f1']:.3f}")

    return pd.DataFrame(results)


def train_final_model(X, y, variant, model_type, config):
    """Train final model on all data for SHAP analysis."""
    X_sel, feature_cols = get_feature_set(X, variant)
    X_sel = X_sel.fillna(0)

    if model_type == "xgboost":
        model = train_xgboost(X_sel, y, config)
    else:
        model = train_lightgbm(X_sel, y, config)

    return model, X_sel, feature_cols
