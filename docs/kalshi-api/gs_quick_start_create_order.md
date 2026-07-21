# Quick Start: Create Your First Order

> Learn how to find markets, place orders, check status, and cancel orders on Kalshi

## Prerequisites

Before you begin, you'll need:

- A Kalshi account with API access configured
- Python with the `requests` and `cryptography` libraries installed
- Your authentication functions set up (see [authentication guide](gs_quick_start_authenticated_requests.md))

## Step 1: Find an Open Market

```python
# Get the first open market (no auth required for public market data)
response = requests.get('https://external-api.demo.kalshi.co/trade-api/v2/markets?limit=1&status=open')
market = response.json()['markets'][0]

print(f"Selected market: {market['ticker']}")
print(f"Title: {market['title']}")
```

## Step 2: Place a Buy Order

```python
import uuid
from urllib.parse import urlparse

def post(private_key, api_key_id, path, data, base_url=BASE_URL):
    """Make an authenticated POST request to the Kalshi API."""
    timestamp = str(int(datetime.datetime.now().timestamp() * 1000))
    sign_path = urlparse(base_url + path).path
    signature = create_signature(private_key, timestamp, "POST", sign_path)

    headers = {
        'KALSHI-ACCESS-KEY': api_key_id,
        'KALSHI-ACCESS-SIGNATURE': signature,
        'KALSHI-ACCESS-TIMESTAMP': timestamp,
        'Content-Type': 'application/json'
    }

    return requests.post(base_url + path, headers=headers, json=data)

# Place a buy order for 1 YES contract at 1 cent
order_data = {
    "ticker": market['ticker'],
    "side": "bid",
    "count": "1",
    "price": "0.0100",
    "time_in_force": "good_till_canceled",
    "self_trade_prevention_type": "taker_at_cross",
    "client_order_id": str(uuid.uuid4())
}

response = post(private_key, API_KEY_ID, '/portfolio/events/orders', order_data)

if response.status_code == 201:
    order = response.json()
    print(f"Order placed successfully!")
    print(f"Order ID: {order['order_id']}")
    print(f"Client Order ID: {order_data['client_order_id']}")
    print(f"Remaining Count: {order['remaining_count']}")
else:
    print(f"Error: {response.status_code} - {response.text}")
```

## Complete Example Script

```python
import requests
import uuid
from urllib.parse import urlparse

# Add POST function to your existing auth code
def post(private_key, api_key_id, path, data, base_url=BASE_URL):
    """Make an authenticated POST request to the Kalshi API."""
    timestamp = str(int(datetime.datetime.now().timestamp() * 1000))
    sign_path = urlparse(base_url + path).path
    signature = create_signature(private_key, timestamp, "POST", sign_path)

    headers = {
        'KALSHI-ACCESS-KEY': api_key_id,
        'KALSHI-ACCESS-SIGNATURE': signature,
        'KALSHI-ACCESS-TIMESTAMP': timestamp,
        'Content-Type': 'application/json'
    }

    return requests.post(base_url + path, headers=headers, json=data)

# Step 1: Find an open market
print("Finding an open market...")
response = requests.get('https://external-api.demo.kalshi.co/trade-api/v2/markets?limit=1&status=open')
market = response.json()['markets'][0]
print(f"Selected: {market['ticker']} - {market['title']}")

# Step 2: Place a buy order
print("\nPlacing order...")
client_order_id = str(uuid.uuid4())
order_data = {
    "ticker": market['ticker'],
    "side": "bid",
    "count": "1",
    "price": "0.0100",
    "time_in_force": "good_till_canceled",
    "self_trade_prevention_type": "taker_at_cross",
    "client_order_id": client_order_id
}

response = post(private_key, API_KEY_ID, '/portfolio/events/orders', order_data)

if response.status_code == 201:
    order = response.json()
    print(f"Order placed successfully!")
    print(f"Order ID: {order['order_id']}")
    print(f"Client Order ID: {client_order_id}")
    print(f"Remaining Count: {order['remaining_count']}")
else:
    print(f"Error: {response.status_code} - {response.text}")
```

## Important Notes

### Client Order ID

The `client_order_id` field is optional, but strongly recommended for order deduplication:

- Generate a unique ID (like UUID4) for each order before submission when you want idempotent retries
- If network issues occur, you can resubmit with the same `client_order_id`
- The API will reject duplicate submissions with the same `client_order_id`, preventing accidental double orders
- Store this ID locally to track orders before receiving the server's `order_id`

### Error Handling

Common errors and how to handle them:

- `401 Unauthorized`: Check your API keys and signature generation
- `400 Bad Request`: Verify your order parameters (price must be 1-99 cents)
- `409 Conflict`: Order with this `client_order_id` already exists
- `429 Too Many Requests`: You've hit the rate limit - slow down your requests
