<!-- frontend/src/lib/ApprovalCard.svelte -->
<script lang="ts">
  import type { ApprovalReq, AgentCaps } from "./wails";
  import { focusOnMount } from "./actions";

  let {
    req, queue, caps, onDecision, onApproveAll, onDenyAll,
  }: {
    req: ApprovalReq;
    queue: ApprovalReq[];
    caps: AgentCaps;
    onDecision: (reqId: string, decision: "allow" | "deny" | "always") => void;
    onApproveAll?: () => void;
    onDenyAll?: () => void;
  } = $props();

  function approveAll() {
    if (onApproveAll) { onApproveAll(); return; }
    for (const r of queue) onDecision(r.reqId, "allow");
  }
  function denyAll() {
    if (onDenyAll) { onDenyAll(); return; }
    for (const r of queue) onDecision(r.reqId, "deny");
  }
</script>

{#if caps.approvals}
  <!-- A docked, bottom-center card (App keeps the sidebar/terminal live), so it is
       a labeled, focus-on-open landmark region — NOT a modal: aria-modal="true"
       would falsely tell AT the rest of the page is inert. -->
  <section aria-label="approval card" class="approval-card"
           tabindex="-1" use:focusOnMount>
    <header class="approval-header">
      <span class="tool-name">{req.tool}</span>
      {#if queue.length > 1}<span class="batch-count">{queue.length} pending</span>{/if}
    </header>
    <p class="approval-summary">{req.summary}</p>
    <div class="approval-actions">
      <button class="btn btn-primary" onclick={() => onDecision(req.reqId, "allow")}>Allow</button>
      <button class="btn" onclick={() => onDecision(req.reqId, "deny")}>Deny</button>
      <button class="btn btn-always" onclick={() => onDecision(req.reqId, "always")}>Always</button>
    </div>
    {#if queue.length > 1}
      <div class="batch-actions">
        <button class="btn btn-sm" onclick={approveAll}>Approve all</button>
        <button class="btn btn-sm" onclick={denyAll}>Deny all</button>
        <span class="batch-badge">{queue.length}</span>
      </div>
    {/if}
  </section>
{/if}

<style>
  /* Floating card — App positions bottom-center; we own the card chrome */
  .approval-card {
    background: var(--perch-glass-bg);
    -webkit-backdrop-filter: var(--perch-glass-filter);
    backdrop-filter: var(--perch-glass-filter);
    color: var(--perch-text);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-lg);
    box-shadow: var(--perch-glass-shadow);
    padding: var(--perch-sp-2) var(--perch-sp-3);
    min-width: 360px;
    max-width: 520px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  /* Header: tool name + optional batch count */
  .approval-header {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    margin-bottom: var(--perch-sp-1);
  }

  /* Tool name — mono accent (identifiers rule) */
  .tool-name {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    color: var(--perch-accent);
    font-weight: 500;
  }

  /* Batch count badge */
  .batch-count {
    font-size: var(--perch-fs-caption);
    color: var(--perch-text-dim);
    margin-left: auto;
  }

  /* Summary text */
  .approval-summary {
    margin: 0 0 var(--perch-sp-2) 0;
    color: var(--perch-text);
    font-size: var(--perch-fs-body);
    line-height: 1.5;
  }

  /* Action row */
  .approval-actions {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    margin-bottom: var(--perch-sp-1);
  }

  /* Shared button base */
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 12px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: 4px;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    cursor: pointer;
    transition: border-color var(--perch-dur) var(--perch-ease),
                color var(--perch-dur) var(--perch-ease),
                background var(--perch-dur) var(--perch-ease);
  }

  .btn:hover {
    border-color: var(--perch-accent);
    color: var(--perch-accent);
  }

  .btn:active {
    background: color-mix(in srgb, var(--perch-accent) 12%, var(--perch-bg));
  }

  .btn:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }

  .btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
    pointer-events: none;
  }

  /* Allow — primary accent fill */
  .btn-primary {
    background: var(--perch-accent);
    color: var(--perch-accent-fg);
    border-color: var(--perch-accent);
  }

  .btn-primary:hover {
    filter: brightness(1.1);
    color: var(--perch-accent-fg);
    border-color: var(--perch-accent);
  }

  .btn-primary:active {
    filter: brightness(0.92);
  }

  /* Always — warn-colored: signals irreversible without red */
  .btn-always {
    color: var(--perch-warn);
    border-color: var(--perch-warn);
  }

  .btn-always:hover {
    background: color-mix(in srgb, var(--perch-warn) 12%, var(--perch-bg));
    color: var(--perch-warn);
    border-color: var(--perch-warn);
  }

  .btn-always:focus-visible {
    outline: 2px solid var(--perch-warn);
    outline-offset: 2px;
  }

  /* Smaller secondary buttons for batch row */
  .btn-sm {
    padding: 2px 8px;
    font-size: var(--perch-fs-caption);
  }

  /* Batch actions row */
  .batch-actions {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-1);
    padding-top: var(--perch-sp-1);
    border-top: 1px solid var(--perch-border);
  }

  /* Queue count badge in batch row */
  .batch-badge {
    margin-left: auto;
    font-size: var(--perch-fs-label);
    font-weight: 600;
    color: var(--perch-text-dim);
    font-family: var(--perch-font-mono);
  }
</style>
