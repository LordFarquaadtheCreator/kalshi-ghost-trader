# Kalshi SDKs

> Official Python and TypeScript SDKs

Kalshi publishes Python and TypeScript SDKs.

> SDKs are updated periodically and may lag the API. Active traders should treat the REST [OpenAPI spec](openapi.yaml) and WebSocket [AsyncAPI spec](asyncapi.yaml) as source of truth. For production, generate your own client from those specs or integrate directly.

## Packages

| Language | Package | Install |
| -------- | ------- | ------- |
| Python (sync) | [kalshi_python_sync](https://pypi.org/project/kalshi_python_sync/) | `pip install kalshi_python_sync` |
| Python (async) | [kalshi_python_async](https://pypi.org/project/kalshi_python_async/) | `pip install kalshi_python_async` |
| TypeScript | [kalshi-typescript](https://www.npmjs.com/package/kalshi-typescript) | `npm install kalshi-typescript` |

> Old `kalshi-python` package is deprecated. Use `kalshi_python_sync` or `kalshi_python_async`.

SDK releases track the OpenAPI spec, generally published Tuesday–Wednesday each week, ahead of corresponding API changes. All SDKs authenticate with API key and RSA-PSS request signing (see [API Keys](gs_api_keys.md)).
