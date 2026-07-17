<script>
  let { label, value, accent = null } = $props();
</script>

<div class="stat-card" style={accent ? `--accent: ${accent}` : ''}>
  <div class="stat-label">{label}</div>
  <div class="stat-value" style={accent ? `color: ${accent}` : ''}>{value}</div>
</div>
