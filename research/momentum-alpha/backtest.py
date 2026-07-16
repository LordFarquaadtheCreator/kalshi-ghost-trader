"""Backtest: simulate trades from model signals.

For each point, model predicts price direction.
If confidence > threshold, enter position.
Exit after horizon_sec.
Compute PnL, hit rate, Sharpe-like ratio.
"""

import numpy as np
import pandas as pd


def simulate_trades(y_true, y_proba, prices_at_entry, prices_at_exit,
                    config, match_labels=None):
    """Simulate trades from model predictions.

    y_proba: P(price up) from model
    prices_at_entry: market price at point time
    prices_at_exit: market price at horizon time
    config: backtest config dict

    Returns dict of trade results.
    """
    bt = config["backtest"]
    min_conf = bt["min_signal_confidence"]
    capital = bt["initial_capital"]
    pos_pct = bt["position_size_pct"]
    fee = bt["fee_cents"] / 100.0  # convert cents to price units

    trades = []
    equity = capital
    equity_curve = [equity]

    for i in range(len(y_proba)):
        prob = y_proba[i]
        entry_price = prices_at_entry[i]
        exit_price = prices_at_exit[i]

        if np.isnan(entry_price) or np.isnan(exit_price):
            equity_curve.append(equity)
            continue

        # signal
        if prob > min_conf:
            # buy YES
            direction = 1
            confidence = prob
        elif prob < (1.0 - min_conf):
            # sell YES (buy NO)
            direction = -1
            confidence = 1.0 - prob
        else:
            equity_curve.append(equity)
            continue

        position_size = capital * pos_pct
        shares = position_size / entry_price if entry_price > 0 else 0

        # PnL: direction * (exit - entry) * shares - fees
        pnl = direction * (exit_price - entry_price) * shares
        pnl -= fee * shares * 2  # entry + exit fees

        equity += pnl
        equity_curve.append(equity)

        trades.append({
            "idx": i,
            "direction": direction,
            "confidence": confidence,
            "entry_price": entry_price,
            "exit_price": exit_price,
            "pnl": pnl,
            "correct": (direction > 0 and exit_price > entry_price) or \
                       (direction < 0 and exit_price < entry_price),
        })

    if not trades:
        return {"n_trades": 0, "final_equity": equity, "return_pct": 0.0}

    trades_df = pd.DataFrame(trades)
    hit_rate = trades_df["correct"].mean()
    total_pnl = trades_df["pnl"].sum()
    win_pnl = trades_df[trades_df["correct"]]["pnl"].sum()
    loss_pnl = trades_df[~trades_df["correct"]]["pnl"].sum()
    avg_win = trades_df[trades_df["correct"]]["pnl"].mean() if win_pnl != 0 else 0
    avg_loss = trades_df[~trades_df["correct"]]["pnl"].mean() if loss_pnl != 0 else 0
    profit_factor = abs(win_pnl / loss_pnl) if loss_pnl != 0 else float("inf")

    # Sharpe-like: mean pnl / std pnl per trade, annualized loosely
    if trades_df["pnl"].std() > 0:
        sharpe = trades_df["pnl"].mean() / trades_df["pnl"].std()
    else:
        sharpe = 0.0

    return {
        "n_trades": len(trades),
        "hit_rate": hit_rate,
        "total_pnl": total_pnl,
        "final_equity": equity,
        "return_pct": (equity - capital) / capital * 100,
        "avg_win": avg_win,
        "avg_loss": avg_loss,
        "profit_factor": profit_factor,
        "sharpe_per_trade": sharpe,
        "max_drawdown": _max_drawdown(equity_curve),
        "equity_curve": equity_curve,
    }


def _max_drawdown(equity_curve):
    """Compute max drawdown from equity curve."""
    peak = equity_curve[0]
    max_dd = 0.0
    for val in equity_curve:
        if val > peak:
            peak = val
        dd = (peak - val) / peak if peak > 0 else 0
        if dd > max_dd:
            max_dd = dd
    return max_dd


def run_backtest_comparison(X, y, groups, prices_entry, prices_exit, config):
    """Run backtest for all model variants.

    Returns DataFrame comparing backtest metrics.
    """
    from models import get_feature_set, train_xgboost, train_lightgbm
    from sklearn.model_selection import GroupKFold

    results = []

    for variant in ["baseline", "momentum_only", "combined"]:
        X_sel, cols = get_feature_set(X, variant)
        X_sel = X_sel.fillna(0)

        # simple split: 80% train, 20% test by group
        n_groups = groups.nunique()
        n_train_groups = max(1, int(n_groups * 0.8))
        unique_groups = groups.unique()
        train_groups = unique_groups[:n_train_groups]
        test_groups = unique_groups[n_train_groups:]

        train_mask = groups.isin(train_groups)
        test_mask = groups.isin(test_groups)

        if test_mask.sum() < 10:
            print(f"  skip {variant}: insufficient test data")
            continue

        X_train, X_test = X_sel[train_mask], X_sel[test_mask]
        y_train = y[train_mask]

        model = train_xgboost(X_train, y_train, config)
        y_proba = model.predict_proba(X_test)[:, 1]

        prices_e = np.array(prices_entry)[test_mask.values]
        prices_x = np.array(prices_exit)[test_mask.values]

        bt_result = simulate_trades(
            y[test_mask], y_proba, prices_e, prices_x, config
        )

        bt_result["variant"] = variant
        results.append(bt_result)

        print(f"  {variant}: {bt_result['n_trades']} trades, "
              f"hit={bt_result['hit_rate']:.2f}, "
              f"return={bt_result['return_pct']:.1f}%, "
              f"sharpe={bt_result['sharpe_per_trade']:.2f}")

    return pd.DataFrame(results)
