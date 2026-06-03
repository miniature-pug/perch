<!-- frontend/src/lib/ApprovalCard.svelte -->
<script lang="ts">
  import type { ApprovalReq, AgentCaps } from "./wails";

  let {
    req, queue, caps, onDecision,
  }: {
    req: ApprovalReq;
    queue: ApprovalReq[];
    caps: AgentCaps;
    onDecision: (reqId: string, decision: "allow" | "deny" | "always") => void;
  } = $props();

  function approveAll() { for (const r of queue) onDecision(r.reqId, "allow"); }
  function denyAll()    { for (const r of queue) onDecision(r.reqId, "deny");  }
</script>

{#if caps.approvals}
  <section aria-label="approval card" class="approval-card">
    <header class="approval-header">
      <span class="tool-name">{req.tool}</span>
      {#if queue.length > 1}<span class="batch-count">{queue.length} pending</span>{/if}
    </header>
    <p class="approval-summary">{req.summary}</p>
    <div class="approval-actions">
      <button onclick={() => onDecision(req.reqId, "allow")}>Allow</button>
      <button onclick={() => onDecision(req.reqId, "deny")}>Deny</button>
      <button onclick={() => onDecision(req.reqId, "always")}>Always</button>
    </div>
    {#if queue.length > 1}
      <div class="batch-actions">
        <button onclick={approveAll}>Approve all</button>
        <button onclick={denyAll}>Deny all</button>
      </div>
    {/if}
  </section>
{/if}
