<script>
  let { text = '', variant = 'default' } = $props();
</script>

<div class="empty-state" class:error={variant === 'error'}>
  {text}
</div>
