<!-- frontend/src/lib/ApprovalCard.svelte -->
<script lang="ts">
  import { onDestroy } from "svelte";
  import type { ApprovalReq, AgentCaps } from "./wails";
  import { focusOnMount } from "./actions";
  import { UNDO_REMOVE_DELAY_MS } from "./constants";

  let {
    req, sessionCount = 1, queue, caps, onDecision, onApproveAll, onDenyAll, onUndoAlways,
  }: {
    req: ApprovalReq;
    // How many requests are queued for THIS session (including the shown head).
    // >1 means the agent has more tools waiting behind this one on this session.
    sessionCount?: number;
    queue: ApprovalReq[];
    caps: AgentCaps;
    onDecision: (reqId: string, decision: "allow" | "deny" | "always") => void;
    onApproveAll?: () => void;
    onDenyAll?: () => void;
    // Optional: host (App) removes the just-granted standing rule when the user
    // hits Undo on the post-grant toast. Optional so the card is self-contained;
    // when absent, Undo simply dismisses the toast.
    onUndoAlways?: (reqId: string) => void;
  } = $props();

  let deciding = $state(false);
  // Post-grant undo affordance for the standing "Always allow" rule: the rule is
  // granted immediately (lowest friction), then a brief toast offers Undo.
  let alwaysUndo = $state(false);
  let undoTimer: ReturnType<typeof setTimeout> | undefined;
  let destroyed = false;

  async function handleDecision(reqId: string, decision: "allow" | "deny" | "always") {
    if (deciding) return;
    deciding = true;
    try { await Promise.resolve(onDecision(reqId, decision)); } finally { deciding = false; }
  }

  async function handleAlways() {
    if (deciding) return;
    try {
      await handleDecision(req.reqId, "always");
    } catch {
      return; // grant failed — no rule created, so nothing to undo
    }
    // The host may have popped this card the moment the grant resolved; only
    // surface the local toast if we're still mounted (App hosts it post-Phase-3).
    if (destroyed) return;
    alwaysUndo = true;
    startUndoTimer();
  }

  function startUndoTimer() {
    clearUndoTimer();
    undoTimer = setTimeout(() => { alwaysUndo = false; undoTimer = undefined; }, UNDO_REMOVE_DELAY_MS);
  }
  function clearUndoTimer() {
    if (undoTimer !== undefined) { clearTimeout(undoTimer); undoTimer = undefined; }
  }
  function undoAlways() {
    clearUndoTimer();
    alwaysUndo = false;
    onUndoAlways?.(req.reqId);
  }
  onDestroy(() => { destroyed = true; clearUndoTimer(); });

  // Single-key accelerators while the card holds focus (Allow is focused on open,
  // so Enter also approves). 'a' = allow, 'd' = deny; the riskier standing grant
  // needs the Shift+A chord, never a lone keypress.
  function handleKeydown(e: KeyboardEvent) {
    if (deciding || alwaysUndo) return;
    const key = e.key.toLowerCase();
    const noMod = !e.ctrlKey && !e.metaKey && !e.altKey;
    if (key === "a" && e.shiftKey && noMod) {
      e.preventDefault();
      handleAlways();
    } else if (key === "a" && !e.shiftKey && noMod) {
      e.preventDefault();
      handleDecision(req.reqId, "allow");
    } else if (key === "d" && !e.shiftKey && noMod) {
      e.preventDefault();
      handleDecision(req.reqId, "deny");
    }
  }

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
       a labeled landmark region, NOT a modal: aria-modal="true" would falsely tell
       assistive tech the rest of the page is inert. Initial focus lands on the
       Allow button so Enter approves and the single-key accelerators work. The
       keydown here is delegation from the focused action buttons (interactive),
       so the accelerators only fire while the card owns focus. -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <section aria-label="approval card" class="approval-card"
           tabindex="-1" onkeydown={handleKeydown}>
    <header class="approval-header">
      <span class="tool-name">{req.tool}</span>
      {#if queue.length > 1}<span class="batch-count">{queue.length} pending</span>{/if}
    </header>
    <!-- Show the actual tool input (scrollable) so nobody approves blind. The tool
         name already lives in the header, so we don't repeat req.summary (which
         leads with the tool name) when the full input is available. -->
    {#if req.input}
      <pre class="approval-input" data-testid="approval-input">{req.input}</pre>
    {:else}
      <p class="approval-summary">{req.summary}</p>
    {/if}
    {#if sessionCount > 1}
      <p class="session-queue-note" data-testid="session-queue-note">
        +{sessionCount - 1} more queued for this session
      </p>
    {/if}
    {#if alwaysUndo}
      <div class="always-undo" role="status" aria-live="polite">
        <span class="undo-msg">Always-allow rule added for {req.tool}.</span>
        <button class="btn btn-sm" onclick={undoAlways} use:focusOnMount>Undo</button>
      </div>
    {:else}
      <div class="approval-actions">
        <button class="btn btn-primary" onclick={() => handleDecision(req.reqId, "allow")} disabled={deciding} use:focusOnMount>Allow</button>
        <button class="btn" onclick={() => handleDecision(req.reqId, "deny")} disabled={deciding}>Deny</button>
        <button class="btn btn-always" onclick={handleAlways} disabled={deciding}
                title="Adds a standing rule so this tool is auto-approved for this agent. You can undo it right after.">Always allow</button>
      </div>
      <p class="accel-hint" aria-hidden="true">
        <kbd>a</kbd> allow · <kbd>d</kbd> deny · <kbd>⇧A</kbd> always allow
      </p>
      {#if queue.length > 1}
        <div class="batch-actions">
          <button class="btn btn-sm" onclick={approveAll}>Approve all</button>
          <button class="btn btn-sm" onclick={denyAll}>Deny all</button>
          <span class="batch-badge">{queue.length}</span>
        </div>
      {/if}
    {/if}
  </section>
{/if}

<style>
  /* Floating card — App positions bottom-center; we own the card chrome */
  .approval-card {
    /* The blocking prompt must stay legible over the terminal, where WebKitGTK
       paints backdrop-filter surfaces transparent over the composited terminal
       subtree. Always solid, never glass. */
    background: var(--perch-glass-bg-solid);
    color: var(--perch-text);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-lg);
    box-shadow: var(--perch-shadow-float);
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

  /* Tool input — the full, exact payload the agent wants to run. Scrollable so a
     large Write/Bash input never blows out the card, and monospace so paths,
     JSON, and shell stay legible. */
  .approval-input {
    margin: 0 0 var(--perch-sp-2) 0;
    padding: var(--perch-sp-1) var(--perch-sp-2);
    max-height: 33vh;
    overflow: auto;
    background: var(--perch-bg);
    border: 1px solid var(--perch-border);
    border-radius: var(--perch-radius-sm);
    color: var(--perch-text);
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
    tab-size: 2;
  }

  /* Post-grant undo toast for the standing "Always allow" rule */
  .always-undo {
    display: flex;
    align-items: center;
    gap: var(--perch-sp-2);
    margin-bottom: var(--perch-sp-1);
    font-size: var(--perch-fs-body);
  }

  .undo-msg {
    color: var(--perch-text);
  }

  .always-undo .btn-sm {
    margin-left: auto;
  }

  /* Keyboard-accelerator hint under the action row */
  .accel-hint {
    margin: var(--perch-sp-1) 0 0 0;
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
  }

  .accel-hint kbd {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-caption);
    color: var(--perch-text);
  }

  /* "N more queued for this session" note */
  .session-queue-note {
    margin: calc(-1 * var(--perch-sp-1)) 0 var(--perch-sp-2) 0;
    color: var(--perch-text-dim);
    font-size: var(--perch-fs-caption);
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
    border-radius: var(--perch-radius-sm);
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
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }

  .btn:disabled {
    opacity: var(--perch-opacity-disabled);
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
    outline: var(--perch-ring-w) solid var(--perch-warn);
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
