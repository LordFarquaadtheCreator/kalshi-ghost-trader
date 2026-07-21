# Kalshi Glossary

> Core terminology used in the Kalshi exchange

**Category:** A high-level discovery grouping for related series, such as sports, crypto, or weather. A series belongs to one category. Use [Get Series List](api-reference/market/get-series-list.md) with the `category` filter to browse series in a category.

**Subcategory:** A narrower discovery grouping within a category. A series can belong to multiple subcategories. In API filters, subcategories are often represented as tags; use [Get Tags for Series Categories](api-reference/search/get-tags-for-series-categories.md) to discover tags grouped by category.

**Market:** A single binary market. This is a low level object which rarely will need to be exposed on its own to members. The usage of the term "market" here is consistent with how it's used in the backend and API.

**Event:** An event is a collection of markets and the basic unit that members should interact with on Kalshi.

**Series:** A series is a collection of related events. The following should hold true for events that make up a series:

- Each event should look at similar data for determination, but translated over another, disjoint time period.
- Series should never have a logical outcome dependency between events.
- Events in a series should have the same ticker prefix.

## Ticker Conventions

Categories and subcategories help organize and filter series, but they are not part of the ticker convention.

Tickers often follow `Series -> Event -> Market`: for example, the `KXHIGHNY` series may have an event like `KXHIGHNY-24JAN01`, and that event may have a market like `KXHIGHNY-24JAN01-T60`. There are occasional exceptions, so do not parse ticker strings to infer relationships. Best practice is to use the series, event, market, and search endpoints and rely on fields like `series_ticker`, `event_ticker`, `category`, and `tags`.
