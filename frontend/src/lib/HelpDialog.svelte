<!-- frontend/src/lib/HelpDialog.svelte -->
<script lang="ts">
  import { trapFocus } from "./actions";
  let {
    open, onClose, section = "all",
  }: {
    open: boolean;
    onClose: () => void;
    // Which panel to show. "all" (default) renders both the shortcuts table and
    // the About blurb. App.svelte can pass "shortcuts" or "about" so the Help
    // menu's two entries open genuinely distinct views instead of the same dialog.
    section?: "shortcuts" | "about" | "all";
  } = $props();

  // Single source of truth for the shortcut table. Each row lists one or more
  // key chips (each rendered as its own <kbd>) joined by `joiner`, plus the
  // action it triggers. Mirrors the bindings in App.svelte's onKeyDown handler
  // and Editor.svelte's Ctrl-S save so the table never drifts from reality.
  type Shortcut = { combos: string[]; joiner?: string; action: string };
  const shortcuts: Shortcut[] = [
    { combos: ["j", "k"], joiner: " / ", action: "Previous / next session" },
    { combos: ["Enter"], action: "Open the selected session" },
    { combos: ["1", "2", "3"], joiner: " / ", action: "Agent / Code / Diff view" },
    { combos: ["g d"], action: "Diff view" },
    { combos: ["g e"], action: "Code view" },
    { combos: ["g t", "g T"], joiner: " / ", action: "Next / previous view" },
    { combos: ["\\"], action: "Toggle split" },
    { combos: ["Ctrl-b"], action: "Toggle sidebar" },
    { combos: ["Ctrl-` (backtick)"], action: "Toggle shell drawer" },
    { combos: ["/"], action: "Filter sessions" },
    { combos: ["Ctrl-K", ":"], joiner: " / ", action: "Command palette" },
    { combos: ["Ctrl-S"], action: "Save file" },
    { combos: ["i"], action: "Enter TERMINAL mode (keys go to the agent)" },
    { combos: ["Ctrl-\\", "Ctrl-n"], joiner: " then ", action: "Leave TERMINAL mode" },
  ];

  function handleKey(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div role="dialog" aria-modal="true" aria-label="help" class="dialog-overlay"
       tabindex="-1" onkeydown={handleKey} use:trapFocus>
    <div class="dialog help-dialog">
      {#if section !== "about"}
      <section class="help-section">
        <h2>Keyboard Shortcuts</h2>
        <table class="shortcuts-table">
          <thead>
            <tr><th>Key</th><th>Action</th></tr>
          </thead>
          <tbody>
            {#each shortcuts as s}
              <tr>
                <td>{#each s.combos as c, i}{#if i > 0}{s.joiner ?? " / "}{/if}<kbd>{c}</kbd>{/each}</td>
                <td>{s.action}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </section>
      {/if}

      {#if section !== "shortcuts"}
      <section class="help-section">
        <h2>About perch</h2>
        <p>perch — a worktree-native cockpit for agent-assisted coding.</p>
      </section>
      {/if}

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
