"""Evaluation: metrics, ROC curves, SHAP feature importance, Sobol sensitivity.

Paper section 4.3 (ROC/eval criteria), 5.2 (SHAP), 6.2 (Sobol).
"""

import numpy as np
import pandas as pd
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from sklearn.metrics import roc_curve, confusion_matrix, ConfusionMatrixDisplay

import shap


def plot_roc_curves(results_dict, outfile):
    """Plot ROC curves for multiple models on same chart.

    results_dict: {label: (y_true, y_proba)}
    """
    fig, ax = plt.subplots(figsize=(8, 6))

    for label, (y_true, y_proba) in results_dict.items():
        fpr, tpr, _ = roc_curve(y_true, y_proba)
        from sklearn.metrics import auc
        roc_auc = auc(fpr, tpr)
        ax.plot(fpr, tpr, label=f"{label} (AUC={roc_auc:.3f})")

    ax.plot([0, 1], [0, 1], "k--", label="Random")
    ax.set_xlabel("False Positive Rate")
    ax.set_ylabel("True Positive Rate")
    ax.set_title("ROC Curves")
    ax.legend()
    ax.grid(True, alpha=0.3)
    plt.tight_layout()
    plt.savefig(outfile, dpi=150)
    plt.close()
    print(f"  saved ROC: {outfile}")


def plot_confusion_matrix(y_true, y_pred, outfile, title="Confusion Matrix"):
    """Plot confusion matrix."""
    cm = confusion_matrix(y_true, y_pred)
    fig, ax = plt.subplots(figsize=(5, 4))
    ConfusionMatrixDisplay(cm, display_labels=["Down/Flat", "Up"]).plot(ax=ax)
    ax.set_title(title)
    plt.tight_layout()
    plt.savefig(outfile, dpi=150)
    plt.close()
    print(f"  saved confusion matrix: {outfile}")


def compute_shap(model, X, feature_cols, outfile, model_type="xgboost"):
    """Compute and plot SHAP feature importance.

    Paper section 5.2.
    """
    if model_type == "xgboost":
        explainer = shap.TreeExplainer(model)
        shap_values = explainer.shap_values(X)
    else:
        explainer = shap.TreeExplainer(model)
        shap_values = explainer.shap_values(X)

    # summary plot
    plt.figure(figsize=(10, 6))
    shap.summary_plot(shap_values, X, feature_names=feature_cols, show=False)
    plt.tight_layout()
    plt.savefig(outfile, dpi=150, bbox_inches="tight")
    plt.close()
    print(f"  saved SHAP: {outfile}")

    # feature importance bar chart
    if isinstance(shap_values, list):
        sv = shap_values[1]  # positive class
    else:
        sv = shap_values

    importance = np.abs(sv).mean(axis=0)
    imp_df = pd.DataFrame({"feature": feature_cols, "importance": importance})
    imp_df = imp_df.sort_values("importance", ascending=False)

    fig, ax = plt.subplots(figsize=(8, 5))
    ax.barh(imp_df["feature"], imp_df["importance"])
    ax.set_xlabel("Mean |SHAP value|")
    ax.set_title("Feature Importance (SHAP)")
    ax.invert_yaxis()
    plt.tight_layout()
    plt.savefig(str(outfile).replace(".png", "_bar.png"), dpi=150)
    plt.close()
    print(f"  saved SHAP bar: {str(outfile).replace('.png', '_bar.png')}")

    return imp_df


def sobol_sensitivity(model, X, feature_cols, n_samples=1000, model_type="xgboost"):
    """Sobol sensitivity analysis via Monte Carlo.

    Paper section 6.2. Uses actual data rows as base, perturbs one feature
    at a time by sampling from observed distribution. Measures variance
    contribution to predictions.

    Returns DataFrame: feature, first_order_index.
    """
    X_arr = X.values
    n_features = X_arr.shape[1]

    # sample base rows from actual data (not means — means give constant predictions)
    idx = np.random.choice(len(X_arr), size=n_samples, replace=True)
    X_base = X_arr[idx].copy()
    base_pred = model.predict_proba(X_base)[:, 1]
    var_total = base_pred.var()

    if var_total < 1e-10:
        return pd.DataFrame({"feature": feature_cols, "first_order": [0.0] * n_features})

    results = []
    for i in range(n_features):
        X_pert = X_base.copy()
        # resample feature i from observed values
        sample_idx = np.random.choice(len(X_arr), size=n_samples, replace=True)
        X_pert[:, i] = X_arr[sample_idx, i]
        pred = model.predict_proba(X_pert)[:, 1]
        # first-order: variance from perturbing only this feature
        first_order = pred.var() / var_total
        results.append({"feature": feature_cols[i], "first_order": first_order})

    df = pd.DataFrame(results).sort_values("first_order", ascending=False)
    return df


def print_results_table(results_df):
    """Print formatted results comparison table."""
    print("\n" + "=" * 80)
    print(f"{'Variant':<16} {'Model':<12} {'Acc':>7} {'AUC':>7} {'F1':>7} "
          f"{'Prec':>7} {'Recall':>7} {'N':>6}")
    print("-" * 80)
    for _, r in results_df.iterrows():
        print(f"{r['variant']:<16} {r['model_type']:<12} "
              f"{r['accuracy']:>7.3f} {r['auc']:>7.3f} {r['f1']:>7.3f} "
              f"{r['precision']:>7.3f} {r['recall']:>7.3f} "
              f"{int(r['n_test']):>6}")
    print("=" * 80)
