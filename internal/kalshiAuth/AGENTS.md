# internal/kalshiAuth

RSA-PSS-SHA256 request signing for Kalshi API.

## Signing format

```
msg = timestamp_ms + method + path
sig = RSA-PSS-SHA256(private_key, msg, salt=hash_length)
headers:
  KALSHI-ACCESS-KEY:       key_id
  KALSHI-ACCESS-SIGNATURE: base64(sig)
  KALSHI-ACCESS-TIMESTAMP: timestamp_ms
```

## Path rules

- REST: full path from API root. `/trade-api/v2/events`, not `/events`.
- WS handshake: always `/trade-api/ws/v2`.
- Strip query params before signing. Sign path only.

## Key formats

Supports PKCS#8 (`PRIVATE KEY`) and PKCS#1 (`RSA PRIVATE KEY`). Kalshi dashboard exports PKCS#8.

## Gotchas

- Timestamp in milliseconds, not seconds.
- PSS salt length = hash length (32 bytes for SHA-256). Matches Python `PSS.DIGEST_LENGTH`.
- MGF1 hash = SHA-256 (Go default when PSSOptions.Hash is 0).
