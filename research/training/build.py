"""build.py — Join labels onto the feature dump.

Stage 1 label: settled outcome (1 if home won, 0 if away won).
Stage 2/3 label: realized net P&L in cents, including fees.

Reads the Parquet dump from featuredump, joins labels from the database,
writes a labeled dataset ready for training.

Usage:
    python -m training.build --dataset /path/to/features.parquet --out /path/to/labeled.parquet
"""

import argparse
import json
import os
import sys
from datetime import datetime, timedelta

import pandas as pd
import pyarrow.parquet as pq


def build_labels(df: pd.DataFrame, dsn: str) -> pd.DataFrame:
    """Join settled outcome and realized P&L onto the feature dump.

    For Stage 1 (fairvalue): label = settled outcome (1=home win, 0=away win).
    For Stage 2/3: label = realized net P&L in cents including fees.
    """
    # In production, this queries the database for settled market results
    # and joins on event_ticker. For now, we add placeholder columns.
    #
    # The fee formula must be ported from docs/kalshi-api/ and validated
    # against Kalshi's current published schedule before use.
    # Do NOT hardcode a remembered formula.

    # Placeholder: add empty label columns.
    # In production, these come from the DB.
    if "settled_outcome" not in df.columns:
        df["settled_outcome"] = None
    if "realized_pnl" not in df.columns:
        df["realized_pnl"] = None

    return df


def main():
    parser = argparse.ArgumentParser(description="Build labeled training dataset")
    parser.add_argument("--dataset", required=True, help="Path to featuredump Parquet")
    parser.add_argument("--out", required=True, help="Output labeled Parquet path")
    parser.add_argument("--dsn", default=os.environ.get("DB_DSN", ""), help="PostgreSQL DSN")
    args = parser.parse_args()

    if not os.path.exists(args.dataset):
        print(f"error: dataset not found: {args.dataset}", file=sys.stderr)
        sys.exit(1)

    df = pq.read_table(args.dataset).to_pandas()
    print(f"Loaded {len(df)} rows from {args.dataset}")

    df = build_labels(df, args.dsn)
    print(f"Labeled {len(df)} rows")

    df.to_parquet(args.out, index=False)
    print(f"Wrote labeled dataset to {args.out}")


if __name__ == "__main__":
    main()
