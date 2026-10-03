<!-- frontend/src/lib/HelpDialog.svelte -->
<script lang="ts">
  import { trapFocus } from "./actions";
  let {
    open, onClose, section = "all",
  }: {
    open: boolean;
    onClose: () => void;
    // Which section to show. The default "all" renders both the shortcuts table
    // and the About text. App.svelte can pass "shortcuts" or "about" so the two
    // Help menu entries open different views, not the same dialog.
    section?: "shortcuts" | "about" | "all";
  } = $props();

  // This is the single source of truth for the shortcut table, grouped by
  // function. Each row lists one or more key chips, each rendered as its own
  // <kbd>, joined by `joiner`, plus the action it triggers. The table mirrors the
  // bindings in App.svelte's onKeyDown handler for global keys, Editor.svelte's
  // Ctrl-S save, and ApprovalCard.svelte's in-card accelerators. This way the
  // table never drifts from the real bindings.
  //
  // Note: there is no shared keymap constant. These strings are kept in sync
  // with App.svelte by hand, and HelpDialog.test.ts guards them. If a binding
  // changes in App.svelte, someone must also change it here, or a test fails.
  type Shortcut = { combos: string[]; joiner?: string; action: string };
  type ShortcutGroup = { heading: string; rows: Shortcut[] };
  const groups: ShortcutGroup[] = [
    { heading: "Navigation", rows: [
      // App.svelte onKeyDown maps j to next (idx + 1) and k to previous (idx - 1).
      { combos: ["j", "k"], joiner: " / ", action: "Next / previous session" },
      { combos: ["Enter"], action: "Open the selected session" },
      { combos: ["/"], action: "Filter sessions" },
    ]},
    { heading: "Sessions", rows: [
      { combos: ["n"], action: "New session" },
      { combos: ["x"], action: "Remove the selected session" },
    ]},
    { heading: "Views & layout", rows: [
      { combos: ["1", "2", "3"], joiner: " / ", action: "Agent / Code / Diff view" },
      { combos: ["g d"], action: "Diff view" },
      { combos: ["g e"], action: "Code view" },
      { combos: ["g t", "g T"], joiner: " / ", action: "Next / previous view" },
      { combos: ["\\"], action: "Toggle split" },
      { combos: ["Ctrl-b"], action: "Toggle sidebar" },
      { combos: ["Ctrl-` (backtick)"], action: "Toggle shell drawer" },
    ]},
    { heading: "Command palette & files", rows: [
      { combos: ["Ctrl-K", ":"], joiner: " / ", action: "Command palette" },
      { combos: ["Ctrl-S"], action: "Save file" },
    ]},
    { heading: "Terminal mode", rows: [
      { combos: ["i"], action: "Enter TERMINAL mode (keys go to the agent)" },
      { combos: ["Ctrl-\\", "Ctrl-n"], joiner: " then ", action: "Leave TERMINAL mode" },
    ]},
    { heading: "Approvals", rows: [
      // ApprovalCard.svelte accelerators. They are active while the approval card has focus.
      // In NORMAL mode, the first a only focuses the card (App.svelte); it never
      // decides. Typing elsewhere never reaches the card.
      { combos: ["a"], action: "Allow the pending request (in NORMAL mode, the first a focuses the card)" },
      { combos: ["d"], action: "Deny the pending request" },
      { combos: ["⇧A"], action: "Always allow this tool" },
    ]},
    { heading: "Help", rows: [
      { combos: ["?", "F1"], joiner: " / ", action: "Open this help" },
    ]},
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
          {#each groups as g}
            <tbody>
              <tr class="group-row"><th colspan="2" scope="colgroup">{g.heading}</th></tr>
              {#each g.rows as s}
                <tr>
                  <td>{#each s.combos as c, i}{#if i > 0}{s.joiner ?? " / "}{/if}<kbd>{c}</kbd>{/each}</td>
                  <td>{s.action}</td>
                </tr>
              {/each}
            </tbody>
          {/each}
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

  /* Group sub-header row. A dim, uppercase section label spans both columns. */
  .shortcuts-table .group-row th {
    padding-top: var(--perch-sp-2);
    font-size: var(--perch-fs-caption);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--perch-text-dim);
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
