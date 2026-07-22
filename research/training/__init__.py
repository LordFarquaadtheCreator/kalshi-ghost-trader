"""Training pipeline for learned strategies (Addendum A.5–A.7).

Nightly flow:
1. featuredump (Go, production feature code) → Parquet
2. build — join labels (settled outcome / realized P&L with fees)
3. train — rolling window with exponential sample weighting
4. evaluate — purged walk-forward CV with embargo + leakage assertion
5. export — artifact + register candidate via API

Usage:
    python -m training.build --dataset /path/to/features.parquet
    python -m training.train --family fairvalue
    python -m training.evaluate
    python -m training.export
"""
