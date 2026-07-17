<script>
  let {
    columns = [],
    rows = [],
    rowClass = null,
    onRowClick = null,
    cell = null,
    children,
  } = $props();
</script>

<div class="table-wrap">
  <table class="data-table">
    <thead>
      <tr>
        {#each columns as col}
          <th class={col.align === 'right' ? 'num' : ''}>{col.label}</th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each rows as row, i}
        <tr
          class={[
            onRowClick ? 'clickable' : '',
            rowClass ? rowClass(row) : '',
          ].filter(Boolean).join(' ')}
          onclick={onRowClick ? () => onRowClick(row) : undefined}
        >
          {#each columns as col}
            <td class={col.class || (col.align === 'right' ? 'num' : '')}>
              {#if cell?.(col, row, i)}
                {@render cell(col, row, i)}
              {:else}
                {row[col.key]}
              {/if}
            </td>
          {/each}
        </tr>
      {/each}
    </tbody>
  </table>
</div>
