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
    // sessionCount counts the requests queued for this session, including the one shown now.
    // A value above 1 means the agent has more tool calls waiting for this session.
    sessionCount?: number;
    queue: ApprovalReq[];
    caps: AgentCaps;
    onDecision: (reqId: string, decision: "allow" | "deny" | "always") => void;
    onApproveAll?: () => void;
    onDenyAll?: () => void;
    // Optional. The host (App) removes the new standing rule when the user selects
    // Undo on the toast shown after grant. This callback is optional so the
    // approval card stays self-contained. If the callback is absent, Undo only
    // closes the toast.
    onUndoAlways?: (reqId: string) => void;
  } = $props();

  let deciding = $state(false);
  // This is the undo control for the standing "Always allow" rule. The approval
  // card grants the rule immediately, for lowest friction. A brief toast then
  // offers Undo.
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
      return; // The grant failed. No rule exists, so there is nothing to undo.
    }
    // The host may remove this approval card as soon as the grant finishes.
    // Show the local toast only if the approval card is still mounted. The App
    // component has hosted this toast since Phase 3.
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

  // These are single-key accelerators. They work while the approval card holds
  // focus. The Allow button gets focus when the card opens, so Enter also
  // approves. The 'a' key allows and the 'd' key denies. The standing grant is
  // riskier, so it needs the Shift+A chord. A lone keypress never triggers it.
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
  <!-- The approval card is docked at the bottom center. The App component keeps
       the sidebar and terminal active. The approval card is a labeled landmark
       region, not a modal. aria-modal="true" would wrongly tell assistive
       technology that the rest of the page is inert. Initial focus lands on the
       Allow button. This lets Enter approve and lets the single-key accelerators
       work. The keydown handler on this section delegates to the focused action
       buttons, which are interactive elements. The accelerators fire only while
       the approval card owns focus. -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <section aria-label="approval card" class="approval-card"
           tabindex="-1" onkeydown={handleKeydown}>
    <header class="approval-header">
      <span class="tool-name">{req.tool}</span>
      {#if queue.length > 1}<span class="batch-count">{queue.length} pending</span>{/if}
    </header>
    <!-- The approval card shows the full tool input in a scrollable area, so the
         user can see it before approval. The header already shows the tool name.
         req.summary also starts with the tool name, so the approval card does
         not repeat req.summary when the full input is available. -->
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
  /* Floating card. The App component sets the position at bottom center.
     ApprovalCard owns the card chrome. */
  .approval-card {
    /* The approval card must stay readable over the terminal. WebKitGTK paints
       backdrop-filter surfaces as transparent over the composited terminal
       subtree. The approval card background is always solid, never glass. */
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

  /* Tool name in mono accent, following the identifier styling rule. */
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

  /* Tool input. This shows the full, exact payload that the agent wants to run.
     This area is scrollable, so a large Write or Bash input never breaks the
     card layout. This area uses a monospace font, so paths, JSON, and shell
     text stay readable. */
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

  /* This is the undo toast shown after the app grants the standing "Always
     allow" rule. */
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

  /* Hint for the keyboard accelerators, shown under the action row */
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

  /* Allow button: primary accent fill */
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

  /* Always allow button: warn-colored. This signals an irreversible action
     without using red. */
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
