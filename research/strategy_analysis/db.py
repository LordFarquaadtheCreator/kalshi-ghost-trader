"""Read-only SQLite access to the live kalshi_tennis.db.

Live DB is being written to. Open with mode=ro so we never block writers
and never accidentally mutate. WAL readers see a consistent snapshot per
connection; long analyses should reuse one connection to keep a stable view.
"""

import os
import sqlite3
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
DB_PATH = REPO / "kalshi_tennis.db"
OUT_DIR = Path(__file__).resolve().parent / "out"
OUT_DIR.mkdir(parents=True, exist_ok=True)


def connect(path=DB_PATH):
    p = str(path)
    uri = f"file:{p}?mode=ro"
    conn = sqlite3.connect(uri, uri=True, timeout=30.0, check_same_thread=False)
    conn.row_factory = sqlite3.Row
    # read-only: pragmas that touch schema are no-ops, but set safe ones
    try:
        conn.execute("PRAGMA query_only=ON")
        conn.execute("PRAGMA journal_mode=WAL")
    except sqlite3.OperationalError:
        pass
    return conn


def save(name, text):
    p = OUT_DIR / name
    p.write_text(text)
    return p


def save_json(name, obj):
    import json
    p = OUT_DIR / name
    with open(p, "w") as f:
        json.dump(obj, f, indent=2, default=str)
    return p


def log(msg):
    print(msg, flush=True)
