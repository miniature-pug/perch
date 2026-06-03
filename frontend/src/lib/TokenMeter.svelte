<!-- frontend/src/lib/TokenMeter.svelte -->
<script lang="ts">
  let { tokens, cost, capsTokens }: { tokens: number; cost: number; capsTokens: boolean } = $props();

  let formattedTokens = $derived(tokens.toLocaleString());
  let formattedCost   = $derived(`$${cost.toFixed(2)}`);
</script>

{#if capsTokens}
  <div role="status" class="token-meter" aria-label="token usage">
    <span class="token-count">{formattedTokens} tok</span>
    <span class="token-cost">{formattedCost}</span>
  </div>
{/if}
