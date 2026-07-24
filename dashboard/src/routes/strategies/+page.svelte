<script>
  import { api } from '$lib/api.js';
  import { browser } from '$app/environment';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import CollapsibleSection from '$lib/components/CollapsibleSection.svelte';
  import MetricsBar from '$lib/components/MetricsBar.svelte';

  let { data } = $props();

  /** @type {string[]} */
  let strategies = $state(data?.strategies ?? []);
  /** @type {string | null} */
  let error = $state(data?.error ?? null);
  let connected = $state(!error);

  async function refresh() {
    try {
      const result = await api.getStrategies();
      strategies = result?.strategies ?? [];
      error = null;
      connected = true;
    } catch (e) {
      error = String(e);
      connected = false;
    }
  }

  // Group strategies by base name (strip variant suffixes for grouping).
  function baseName(/** @type {string} */ s) {
    for (const sep of ['-itf', '-wta', '-atp', '-challenger', '-doubles', '-evening',
      '-noon', '-series', '-eu-daytime', '-itfwdoubles', '-set1', '-strict',
      '-deep', '-elite', '-aggro', '-noadjust', '-serve', '-cheap', '-favorite']) {
      if (s.endsWith(sep)) return s.slice(0, -sep.length);
    }
    return s;
  }

  let groups = $derived.by(() => {
    /** @type {Record<string, string[]>} */
    const g = {};
    for (const s of strategies) {
      const b = baseName(s);
      if (!g[b]) g[b] = [];
      g[b].push(s);
    }
    // Sort groups alphabetically, variants within each group alphabetically.
    /** @type {[string, string[]][]} */
    const entries = Object.entries(g).sort(([a], [b]) => a.localeCompare(b));
    for (const [, variants] of entries) {
      variants.sort();
    }
    return entries;
  });

  // --- Aggregate stats ---
  let multiVariantGroups = $derived(groups.filter(([, v]) => v.length > 1).length);
  let singleStrategies = $derived(groups.filter(([, v]) => v.length === 1).length);
  let largestGroup = $derived.by(() => {
    if (groups.length === 0) return null;
    let best = groups[0];
    for (const g of groups) if (g[1].length > best[1].length) best = g;
    return best;
  });
  let avgVariants = $derived(groups.length > 0 ? (strategies.length / groups.length).toFixed(1) : '0');
</script>

<svelte:head><title>Strategies — Kalshi Ghost Trader</title></svelte:head>

<div class="page-container">
  <PageHeader title="Strategies" {connected} error={error || undefined}>
    <button onclick={refresh} class="refresh-btn">Refresh</button>
  </PageHeader>

  {#if strategies.length === 0 && !error}
    <EmptyState text="No strategies registered." />
  {:else if error}
    <EmptyState text={error} variant="error" />
  {:else}
    <MetricsBar
      primary={[
        { label: 'Total', value: strategies.length },
        { label: 'Base', value: groups.length },
        { label: 'Avg Variants', value: avgVariants },
        { label: 'Multi-Variant', value: multiVariantGroups },
        { label: 'Single', value: singleStrategies },
        { label: 'Largest', value: largestGroup ? `${largestGroup[0]} (${largestGroup[1].length})` : '\u2014' },
      ]}
    />

    <CollapsibleSection title="Registered Strategies" count={strategies.length} defaultOpen={true}>
      <div class="strategy-groups">
        {#each groups as [base, variants]}
          <div class="strategy-group">
            <div class="group-header" class:multi={variants.length > 1}>
              <span class="group-name">{base}</span>
              {#if variants.length > 1}
                <span class="variant-count">{variants.length} variants</span>
              {/if}
            </div>
            <div class="variant-list">
              {#each variants as v}
                <span class="strategy-tag">{v}</span>
              {/each}
            </div>
          </div>
        {/each}
      </div>
      <div class="groups-footer">
        {strategies.length} strategies across {groups.length} base groups
        — {multiVariantGroups} multi-variant, {singleStrategies} single
      </div>
    </CollapsibleSection>
  {/if}
</div>

<style>
  .page-container { padding: 20px; }
  .groups-footer {
    margin-top: 12px;
    padding: 10px 14px;
    background: var(--surface-hover);
    border-top: 2px solid var(--border-strong);
    border-radius: var(--radius-sm);
    font-size: 13px;
    color: var(--text-muted);
  }
  .refresh-btn {
    padding: 4px 12px;
    font-size: 12px;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--surface);
    cursor: pointer;
  }
  .refresh-btn:hover { background: var(--border); }
  .strategy-groups {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .strategy-group {
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 10px 14px;
  }
  .group-header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 6px;
  }
  .group-name {
    font-weight: 600;
    font-size: 14px;
    color: var(--text);
  }
  .variant-count {
    font-size: 11px;
    color: var(--text-muted);
    background: var(--surface-hover);
    padding: 2px 8px;
    border-radius: 10px;
  }
  .variant-list {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .strategy-tag {
    font-family: monospace;
    font-size: 12px;
    padding: 3px 10px;
    border-radius: var(--radius-xs);
    background: var(--surface-hover);
    border: 1px solid var(--border);
    color: var(--text-muted);
  }
  .group-header.multi .group-name {
    color: var(--text);
  }
</style>
