"""HMM momentum model following paper section 3.

Hidden states = momentum regimes (negative, neutral, positive).
Observations = point-level features (server, scorer, score diff).

HMM gives P(momentum state | point sequence).
EMA smooths state probability into continuous momentum value.
"""

import numpy as np
import pandas as pd
from hmmlearn.hmm import GaussianHMM


def build_observations(points_df):
    """Build observation matrix from points.

    Features per point:
    - server (1=home, 2=away) -> -1/+1
    - scorer (1=home, 2=away) -> -1/+1 (did server win?)
    - score_diff_games (home - away)
    - score_diff_sets (home - away)
    - is_break_point (0/1)

    Returns (X, lengths) for HMM. X is stacked across all matches.
    """
    obs = []
    lengths = []
    for _, p in points_df.iterrows():
        server_sign = 1.0 if p["server"] == 1 else -1.0
        # did server win the point?
        server_won = 1.0 if p["scorer"] == p["server"] else -1.0
        score_diff_games = float(p["home_games"] - p["away_games"])
        home_sets = p.get("home_set_games", 0) or 0
        away_sets = p.get("away_set_games", 0) or 0
        score_diff_sets = float(home_sets - away_sets)
        is_break = float(p.get("is_break_point", 0) or 0)

        obs.append([server_sign, server_won, score_diff_games, score_diff_sets, is_break])

    X = np.array(obs, dtype=np.float64)
    lengths = [len(X)]
    return X, lengths


def build_observations_multi(matches_points):
    """Build observation matrix from multiple matches for HMM training.

    matches_points: list of points DataFrames.
    Returns (X, lengths) stacked.
    """
    all_obs = []
    lengths = []
    for points_df in matches_points:
        if points_df.empty:
            continue
        X_single, _ = build_observations(points_df)
        all_obs.append(X_single)
        lengths.append(len(X_single))

    if not all_obs:
        return np.empty((0, 5)), []

    X = np.vstack(all_obs)
    return X, lengths


def train_hmm(X, lengths, n_states=3, n_iter=100, random_state=42):
    """Train Gaussian HMM on observation sequences.

    n_states=3: negative momentum, neutral, positive momentum.
    Returns fitted model.
    """
    model = GaussianHMM(
        n_components=n_states,
        covariance_type="diag",
        n_iter=n_iter,
        random_state=random_state,
        tol=1e-4,
    )
    model.fit(X, lengths=lengths)
    return model


def infer_states(model, X):
    """Infer posterior state probabilities for each observation.

    Returns array (n_points, n_states) of P(state | observation).
    """
    posteriors = model.predict_proba(X)
    return posteriors


def momentum_from_states(posteriors, n_states=3):
    """Convert HMM posteriors to scalar momentum.

    States ordered by mean server_won (fitted). State with highest
    mean = positive momentum for home player.

    Returns array (n_points,) of momentum in range [-1, +1].
    """
    # assign each state a momentum weight: -1, 0, +1
    # states sorted by their emission mean on server_won dimension
    # (dim 1 in our observation vector)
    state_means = model_means_sorted(posteriors)
    # we'll use the middle state as neutral
    weights = np.linspace(-1.0, 1.0, n_states)

    # posteriors @ weights = expected momentum
    momentum = posteriors @ weights
    return momentum


_model_cache = {}


def model_means_sorted(posteriors):
    """Placeholder — actual sorting done in compute_momentum."""
    return None


def compute_momentum(model, X, beta=0.85):
    """Full momentum pipeline: HMM posteriors -> state weighting -> EMA.

    Returns (momentum_raw, momentum_ema) arrays.
    """
    posteriors = infer_states(model, X)

    # sort states by emission mean on "server_won" dimension (index 1)
    means = model.means_[:, 1]  # server_won column
    order = np.argsort(means)  # ascending: worst to best momentum

    n_states = model.n_components
    weights = np.linspace(-1.0, 1.0, n_states)
    # reorder weights to match sorted states
    state_weights = np.zeros(n_states)
    for rank, state_idx in enumerate(order):
        state_weights[state_idx] = weights[rank]

    # raw momentum = expected weight
    momentum_raw = posteriors @ state_weights

    # EMA smoothing
    momentum_ema = ema(momentum_raw, beta)

    return momentum_raw, momentum_ema


def ema(values, beta=0.85):
    """Exponential moving average. Paper eq 7.

    v_t = beta * v_{t-1} + (1-beta) * theta_t
    """
    v = np.zeros(len(values))
    v[0] = values[0]
    for t in range(1, len(values)):
        v[t] = beta * v[t - 1] + (1.0 - beta) * values[t]
    return v


def compute_court_features(points_df, model=None, beta=0.85, n_states=3):
    """Compute all court-level features for a single match.

    Returns DataFrame indexed same as points_df with:
    - hmm_state: most likely state (0..n_states-1)
    - momentum_raw: HMM-derived momentum before smoothing
    - momentum_ema: EMA-smoothed momentum
    - serve_win_rate_home: rolling home serve win rate
    - serve_win_rate_away: rolling away serve win rate
    - break_rate_home: rolling home break rate
    - break_rate_away: rolling away break rate
    - score_diff_games: home_games - away_games
    - score_diff_sets: home_set_games - away_set_games
    - points_into_game: point_number within current game
    - is_break_point: 0/1
    - is_server_home: 1 if server == 1
    """
    if points_df.empty:
        return pd.DataFrame()

    X, _ = build_observations(points_df)

    if model is None:
        model = train_hmm(X, [len(X)], n_states=n_states)

    posteriors = infer_states(model, X)
    hmm_states = np.argmax(posteriors, axis=1)

    # sort states by emission mean on server_won dimension
    means = model.means_[:, 1]
    order = np.argsort(means)
    weights = np.linspace(-1.0, 1.0, n_states)
    state_weights = np.zeros(n_states)
    for rank, state_idx in enumerate(order):
        state_weights[state_idx] = weights[rank]

    momentum_raw = posteriors @ state_weights
    momentum_ema = ema(momentum_raw, beta)

    # rolling serve win rates (cumulative)
    df = points_df.copy().reset_index(drop=True)
    df["server_won"] = (df["scorer"] == df["server"]).astype(int)
    df["home_serving"] = (df["server"] == 1).astype(int)
    df["away_serving"] = (df["server"] == 2).astype(int)
    df["home_serve_won"] = ((df["server"] == 1) & (df["scorer"] == 1)).astype(int)
    df["away_serve_won"] = ((df["server"] == 2) & (df["scorer"] == 2)).astype(int)

    # cumulative serve win rates
    home_serve_count = df["home_serving"].cumsum()
    away_serve_count = df["away_serving"].cumsum()
    home_serve_wins = df["home_serve_won"].cumsum()
    away_serve_wins = df["away_serve_won"].cumsum()

    df["serve_win_rate_home"] = np.where(
        home_serve_count > 0, home_serve_wins / home_serve_count, 0.5
    )
    df["serve_win_rate_away"] = np.where(
        away_serve_count > 0, away_serve_wins / away_serve_count, 0.5
    )

    # break rate: fraction of return games won
    # home breaks = away serving + home scored
    df["home_break"] = ((df["server"] == 2) & (df["scorer"] == 1)).astype(int)
    df["away_break"] = ((df["server"] == 1) & (df["scorer"] == 2)).astype(int)
    home_return_games = df["away_serving"].cumsum()
    away_return_games = df["home_serving"].cumsum()
    df["break_rate_home"] = np.where(
        home_return_games > 0, df["home_break"].cumsum() / home_return_games, 0.0
    )
    df["break_rate_away"] = np.where(
        away_return_games > 0, df["away_break"].cumsum() / away_return_games, 0.0
    )

    # score differentials
    df["score_diff_games"] = df["home_games"].astype(float) - df["away_games"].astype(float)
    home_sets = df.get("home_set_games", pd.Series([0] * len(df)))
    away_sets = df.get("away_set_games", pd.Series([0] * len(df)))
    df["score_diff_sets"] = pd.to_numeric(home_sets, errors="coerce").fillna(0).astype(float) - \
                            pd.to_numeric(away_sets, errors="coerce").fillna(0).astype(float)

    df["points_into_game"] = df["point_number"]
    df["is_break_point"] = df.get("is_break_point", 0).fillna(0).astype(int)
    df["is_server_home"] = (df["server"] == 1).astype(int)

    # momentum
    df["hmm_state"] = hmm_states
    df["momentum_raw"] = momentum_raw
    df["momentum_ema"] = momentum_ema

    return df
