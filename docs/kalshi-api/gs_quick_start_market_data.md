# Quick Start: Market Data

> Learn how to access real-time market data without authentication

## Making Unauthenticated Requests

Kalshi provides several public endpoints that don't require API keys. These endpoints allow you to access market data directly from our production servers at `https://external-api.kalshi.com/trade-api/v2`.

> **Note about the API URL**: Despite the "elections" subdomain, the production Trade API provides access to ALL Kalshi markets - not just election-related ones. This includes markets on economics, climate, technology, entertainment, and more.

> No authentication headers are required for the endpoints in this guide. You can start making requests immediately!

## Step 1: Get Series Information

```python
import requests

# Get series information for KXHIGHNY
url = "https://external-api.kalshi.com/trade-api/v2/series/KXHIGHNY"
response = requests.get(url)
series_data = response.json()

print(f"Series Title: {series_data['series']['title']}")
print(f"Frequency: {series_data['series']['frequency']}")
print(f"Category: {series_data['series']['category']}")
```

```javascript
fetch('https://external-api.kalshi.com/trade-api/v2/series/KXHIGHNY')
  .then(response => response.json())
  .then(data => {
    console.log(`Series Title: ${data.series.title}`);
    console.log(`Frequency: ${data.series.frequency}`);
    console.log(`Category: ${data.series.category}`);
  });
```

```bash
curl -X GET "https://external-api.kalshi.com/trade-api/v2/series/KXHIGHNY"
```

## Step 2: Get Today's Events and Markets

```python
# Get all open markets for the KXHIGHNY series
markets_url = f"https://external-api.kalshi.com/trade-api/v2/markets?series_ticker=KXHIGHNY&status=open"
markets_response = requests.get(markets_url)
markets_data = markets_response.json()

print(f"\nActive markets in KXHIGHNY series:")
for market in markets_data['markets']:
    print(f"- {market['ticker']}: {market['title']}")
    print(f"  Event: {market['event_ticker']}")
    print(f"  Yes Price: ${market['yes_bid_dollars']} | Volume: {market['volume_fp']}")
    print()

# Get details for a specific event if you have its ticker
if markets_data['markets']:
    event_ticker = markets_data['markets'][0]['event_ticker']
    event_url = f"https://external-api.kalshi.com/trade-api/v2/events/{event_ticker}"
    event_response = requests.get(event_url)
    event_data = event_response.json()

    print(f"Event Details:")
    print(f"Title: {event_data['event']['title']}")
    print(f"Category: {event_data['event']['category']}")
```

## Step 3: Get Orderbook Data

```python
# Get orderbook for a specific market
if not markets_data['markets']:
    raise ValueError("No open markets found. Try removing status=open or choose another series.")

market_ticker = markets_data['markets'][0]['ticker']
orderbook_url = f"https://external-api.kalshi.com/trade-api/v2/markets/{market_ticker}/orderbook"

orderbook_response = requests.get(orderbook_url)
orderbook_data = orderbook_response.json()

print(f"\nOrderbook for {market_ticker}:")
print("YES BIDS:")
for price_dollars, count_fp in orderbook_data['orderbook_fp']['yes_dollars'][:5]:
    print(f"  Price: ${price_dollars}, Quantity: {count_fp}")

print("\nNO BIDS:")
for price_dollars, count_fp in orderbook_data['orderbook_fp']['no_dollars'][:5]:
    print(f"  Price: ${price_dollars}, Quantity: {count_fp}")
```

## Working with Large Datasets

The Kalshi API uses cursor-based pagination to handle large datasets efficiently. See [Understanding Pagination](gs_pagination.md).

## Understanding Orderbook Responses

Kalshi's orderbook structure is unique due to the nature of binary prediction markets. The API only returns bids (not asks) because of the reciprocal relationship between YES and NO positions. See [Orderbook Responses](gs_orderbook_responses.md).
