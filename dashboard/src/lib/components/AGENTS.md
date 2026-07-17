# src/lib/components — Shared Svelte Components

Reusable Svelte 5 components. All use runes (`$props`, `$state`). No TypeScript — JSDoc for types.

## Components

### PageHeader.svelte
Page title + connection status + error badge. Props: `title`, `connected`, `error`. Snippet slot for extra badges (loading state, last run time, etc.).

### StatCard.svelte
Single stat card. Props: `label`, `value`. Used in `.stats-grid` layouts.

### Badge.svelte
Colored pill indicator. Props: `variant` (`ok` | `err` | `pending` | `loading`), `text`.

### EmptyState.svelte
Centered placeholder for empty/loading/error states. Props: `text`, `variant` (`default` | `error`).

### DataTable.svelte
Generic table from config. Props: `columns` (array of `{key, label, class?, align?}`), `rows` (array of objects), `rowClick?` handler. Not currently used by any page — pages build tables inline for more control. Available if needed.

### LineChart.svelte
Chart.js line chart bound to a polling store. Props: `title`, `series` (array of `{label, getValue, color}`), `store` (readable with `.history` array), `yUnit?`. Renders chart on store updates, destroys on unmount.

### BarChart.svelte
Chart.js bar chart. Props: `title`, `labels`, `datasets`, `yLabel?`. Used for win/loss comparison on strategies page.

### CollapsibleSection.svelte
Collapsible wrapper for table sections. Props: `title`, `count?`, `defaultOpen?` (default true). Button header with arrow icon (▼/▶). Content via snippet children. Used across all pages with tables.

## Conventions

- Import from `$lib/components/Name.svelte`.
- CSS uses variables from `$lib/styles.css`.
- No hardcoded colors — use `var(--win)`, `var(--loss)`, `var(--surface)`, etc.
