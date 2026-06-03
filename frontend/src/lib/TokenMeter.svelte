<!-- frontend/src/lib/TokenMeter.svelte -->
<script lang="ts">
  let { tokens, cost, capsTokens }: { tokens: number; cost: number; capsTokens: boolean } = $props();

  function formatTokens(n: number): string {
    if (n < 1000) return String(n);
    if (n < 1_000_000) {
      const s = (n / 1000).toFixed(1);
      return s.endsWith(".0") ? s.slice(0, -2) + "k" : s + "k";
    }
    const s = (n / 1_000_000).toFixed(1);
    return s.endsWith(".0") ? s.slice(0, -2) + "M" : s + "M";
  }

  let formattedTokens = $derived(formatTokens(tokens));
  let formattedCost   = $derived(`$${cost.toFixed(2)}`);
</script>

<div role="status" class="token-meter" aria-label="token usage">
  <span class="token-count">{formattedTokens} tok</span>
  <span class="token-cost">{formattedCost}</span>
</div>

<style>
  /* ── Token meter — compact status-line item ───────────────────── */
  .token-meter {
    display: inline-flex;
    align-items: center;
    gap: 0;
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    white-space: nowrap;
    flex-shrink: 0;
  }

  /* "·" separator injected before the cost with no markup change */
  .token-cost::before {
    content: " · ";
  }
</style>
