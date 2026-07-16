"""Court features from point-by-point data.

Implements paper's momentum model:
1. HMM on point sequences to infer hidden momentum state
2. EMA smoothing of HMM state probability
3. Rolling serve win rate, break rate, score state
"""
