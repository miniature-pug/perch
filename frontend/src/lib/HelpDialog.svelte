<!-- frontend/src/lib/HelpDialog.svelte -->
<script lang="ts">
  import { trapFocus } from "./actions";
  let {
    open, onClose,
  }: {
    open: boolean;
    onClose: () => void;
  } = $props();

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div role="dialog" aria-modal="true" aria-label="help" class="dialog-overlay"
       tabindex="-1" onkeydown={handleKey} use:trapFocus>
    <div class="dialog help-dialog">
      <section class="help-section">
        <h2>Keyboard Shortcuts</h2>
        <table class="shortcuts-table">
          <thead>
            <tr><th>Key</th><th>Action</th></tr>
          </thead>
          <tbody>
            <tr><td><kbd>j</kbd> / <kbd>k</kbd></td><td>Previous / next session</td></tr>
            <tr><td><kbd>Enter</kbd></td><td>Open the selected session</td></tr>
            <tr><td><kbd>1</kbd> / <kbd>2</kbd> / <kbd>3</kbd></td><td>Agent / Code / Diff view</td></tr>
            <tr><td><kbd>g d</kbd></td><td>Diff view</td></tr>
            <tr><td><kbd>g e</kbd></td><td>Code view</td></tr>
            <tr><td><kbd>\</kbd></td><td>Toggle split</td></tr>
            <tr><td><kbd>Ctrl-` (backtick)</kbd></td><td>Toggle shell drawer</td></tr>
            <tr><td><kbd>/</kbd></td><td>Filter sessions</td></tr>
            <tr><td><kbd>:</kbd></td><td>Command palette</td></tr>
            <tr><td><kbd>i</kbd></td><td>Enter TERMINAL mode (keys go to the agent)</td></tr>
            <tr><td><kbd>Ctrl-\</kbd> then <kbd>Ctrl-n</kbd></td><td>Leave TERMINAL mode</td></tr>
          </tbody>
        </table>
      </section>

      <section class="help-section">
        <h2>About perch</h2>
        <p>perch — a worktree-native cockpit for agent-assisted coding.</p>
      </section>

      <button aria-label="close help" onclick={onClose}>Close</button>
    </div>
  </div>
{/if}

<style>
  .dialog-overlay {
    position: fixed;
    inset: 0;
    background: var(--perch-scrim);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: var(--perch-z-modal);
  }

  .dialog {
    background: var(--perch-surface);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-lg);
    box-shadow: var(--perch-shadow-float);
    padding: var(--perch-sp-3);
    min-width: 480px;
    max-width: 640px;
    max-height: 80vh;
    overflow-y: auto;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  .help-section {
    margin-bottom: var(--perch-sp-2);
  }

  .help-section h2 {
    font-size: var(--perch-fs-body);
    font-weight: 600;
    margin: 0 0 var(--perch-sp-1) 0;
    color: var(--perch-text);
    border-bottom: 1px solid var(--perch-border);
    padding-bottom: 4px;
  }

  .shortcuts-table {
    border-collapse: collapse;
    width: 100%;
    margin-bottom: var(--perch-sp-1);
  }

  .shortcuts-table th,
  .shortcuts-table td {
    text-align: left;
    padding: 3px 8px;
    font-size: var(--perch-fs-body);
  }

  .shortcuts-table th {
    font-weight: 600;
    color: var(--perch-text);
    border-bottom: 1px solid var(--perch-border);
  }

  .shortcuts-table kbd {
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    background: var(--perch-bg-alt, var(--perch-bg));
    border: 1px solid var(--perch-border);
    border-radius: 3px;
    padding: 1px 4px;
  }

  .help-section p {
    margin: 0;
    color: var(--perch-text);
  }

  button[aria-label="close help"] {
    margin-top: var(--perch-sp-2);
    padding: 4px 12px;
    background: var(--perch-bg);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-sm);
    font-size: var(--perch-fs-body);
    cursor: pointer;
  }
</style>
