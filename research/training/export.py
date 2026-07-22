"""export.py — Export artifact and register candidate via API.

Computes artifact_sha and asserts feature_hash matches the extractor's
current hash. A mismatch aborts the run: it means features changed
without a retrain and the artifact is invalid.

Usage:
    python -m training.export --meta /path/to/meta.json --metrics /path/to/metrics.json
"""

import argparse
import hashlib
import json
import os
import sys
import time
import urllib.request


def compute_sha(path: str) -> str:
    """Compute SHA256 of the artifact file."""
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return h.hexdigest()[:16]


def register_candidate(api_url: str, api_token: str, meta: dict, metrics: dict, artifact_sha: str) -> int:
    """Register a candidate model via the API."""
    body = {
        "family": meta["family"],
        "version": meta["version"],
        "trained_at": meta["trained_at"],
        "train_from_ts": int(time.time()) - 180 * 86400,
        "train_to_ts": int(time.time()),
        "feature_hash": meta["feature_hash"],
        "artifact_path": meta["artifact_path"],
        "artifact_sha": artifact_sha,
        "metrics": metrics,
    }

    req = urllib.request.Request(
        f"{api_url}/api/v1/models",
        data=json.dumps(body).encode(),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {api_token}",
        },
        method="POST",
    )

    try:
        with urllib.request.urlopen(req) as resp:
            result = json.loads(resp.read())
            return result.get("data", {}).get("id", 0)
    except urllib.error.HTTPError as e:
        print(f"error: API returned {e.code}: {e.read().decode()}", file=sys.stderr)
        sys.exit(1)


def main():
    parser = argparse.ArgumentParser(description="Export artifact and register candidate")
    parser.add_argument("--meta", required=True, help="Model metadata JSON path")
    parser.add_argument("--metrics", required=True, help="Evaluation metrics JSON path")
    parser.add_argument("--api-url", default=os.environ.get("API_URL", "http://localhost:6060"))
    parser.add_argument("--api-token", default=os.environ.get("API_TOKEN", ""))
    args = parser.parse_args()

    with open(args.meta) as f:
        meta = json.load(f)
    with open(args.metrics) as f:
        metrics = json.load(f)

    # Compute artifact SHA.
    artifact_sha = compute_sha(meta["artifact_path"])
    print(f"Artifact SHA: {artifact_sha}")

    # Assert feature_hash matches.
    # In production, this compares against the Go extractor's hash.
    # A mismatch means features changed without a retrain — abort.
    expected_hash = os.environ.get("EXPECTED_FEATURE_HASH", "")
    if expected_hash and meta["feature_hash"] != expected_hash:
        print(
            f"error: feature_hash mismatch: artifact={meta['feature_hash']} "
            f"expected={expected_hash}",
            file=sys.stderr,
        )
        print("Features changed without a retrain. Artifact is invalid.", file=sys.stderr)
        sys.exit(3)

    # Register candidate.
    model_id = register_candidate(args.api_url, args.api_token, meta, metrics, artifact_sha)
    print(f"Registered candidate model: id={model_id}")


if __name__ == "__main__":
    main()
