<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon }  from "@xterm/addon-fit";
  import { onPtyData, onPtyExit, writeToPty, resizePty, clipboardSetText, clipboardText } from "./wails";
  import { TERMINAL_SCROLLBACK, PTY_MAX_DIM } from "./constants";
  import { makeTerminalKeyHandler } from "./terminalKeys";

  // `visible` mirrors the pattern that Editor, FileTree, and Preview already
  // use: the pane stays mounted and is merely hidden by an ancestor
  // `display:none` on collapse or tab switch. A ResizeObserver never fires
  // for an ancestor display toggle. Without this signal, the terminal's cols
  // and rows go stale while hidden, and the cursor scrolls out of view when
  // shown again. The effect below re-fits on the hidden-to-visible edge to
  // correct that.
  let { paneId, cwd, onExit, visible = true }:
    { paneId: string; cwd: string; onExit?: (code: number) => void; visible?: boolean } = $props();

  // Hex-alpha suffix for the xterm text-selection layer. color-mix() is not
  // usable as a raw ITheme value, so this suffix is appended to the accent
  // hex instead. 0x66 is about 40% opacity: enough to tint the selection
  // without hiding the glyphs underneath.
  const SELECTION_ALPHA_HEX = "66";

  // Trailing-edge debounce for the pty resize. A splitter drag fires the
  // ResizeObserver dozens of times per second. Each resizePty call is a
  // SIGWINCH that the agent TUI reflows on, so a raw storm of calls makes it
  // stutter. This code coalesces fit() into one layout pass per animation
  // frame, and sends the resize only once the drag settles and only when the
  // dimensions actually changed. 80ms spans a drag's frame cadence, yet still
  // feels instant on release.
  const PTY_RESIZE_DEBOUNCE_MS = 80;

  let host:     HTMLDivElement;
  let term:     Terminal;
  let fit:      FitAddon;
  let offData:  (() => void) | undefined;
  let offExit:  (() => void) | undefined;
  let obs:      ResizeObserver | undefined;
  let themeObs: MutationObserver | undefined;
  let disposed = false;

  // Resize coalescing state: one pending rAF for fit(), one trailing-edge
  // timer for the pty resize, and the last cols/rows this component actually
  // sent, so an unchanged observation is a no-op. -1 is an impossible
  // dimension, so the first real measurement always sends.
  let rafId:       number | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let lastCols = -1;
  let lastRows = -1;

  // Pending frames for the deferred initial fit, a double rAF that mirrors
  // the become-visible effect. This is tracked so onDestroy can cancel a
  // still-queued fit; it must never run after teardown.
  let initRaf1: number | undefined;
  let initRaf2: number | undefined;

  // Right-click Copy/Paste menu, modelled on FileTree's context menu.
  // `canCopy` is captured at open time from term.hasSelection(), so the Copy
  // item's enabled state stays stable while the menu is up. Approximate
  // dimensions clamp the menu inside the viewport, the same as FileTree.
  const MENU_APPROX_W = 140;
  const MENU_APPROX_H = 80;
  let menu = $state<{ x: number; y: number; canCopy: boolean } | null>(null);

  /** Read a CSS custom property from :root, returning a trimmed string or fallback. */
  function cssVar(name: string, fallback: string): string {
    const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    return v || fallback;
  }

  /** Build an xterm ITheme from the active design-token palette. */
  function buildXtermTheme() {
    return {
      background:        cssVar("--perch-bg",          "#282828"),
      foreground:        cssVar("--perch-text",         "#ebdbb2"),
      cursor:            cssVar("--perch-accent",       "#d79921"),
      cursorAccent:      cssVar("--perch-bg",           "#282828"),
      selectionBackground: (() => {
        const accent = cssVar("--perch-accent", "#d79921");
        return accent.startsWith("#") && accent.length === 7 ? accent + SELECTION_ALPHA_HEX : accent;
      })(),
      // Standard 16-color ANSI. Red, green, yellow and the greys come from the
      // semantic tokens. Blue, magenta and cyan have their own per-theme
      // --perch-ansi-* tokens, so blue and cyan are no longer the same color
      // and magenta is no longer the (orange) accent (FEX-32).
      black:             cssVar("--perch-bg-elev",      "#1d2021"),
      red:               cssVar("--perch-err",          "#fb4934"),
      green:             cssVar("--perch-ok",           "#b8bb26"),
      yellow:            cssVar("--perch-warn",         "#fabd2f"),
      blue:              cssVar("--perch-ansi-blue",    "#83a598"),
      magenta:           cssVar("--perch-ansi-magenta", "#d3869b"),
      cyan:              cssVar("--perch-ansi-cyan",    "#8ec07c"),
      white:             cssVar("--perch-text",         "#ebdbb2"),
      brightBlack:       cssVar("--perch-text-dim",     "#a89984"),
      brightRed:         cssVar("--perch-err",          "#fb4934"),
      brightGreen:       cssVar("--perch-ok",           "#b8bb26"),
      brightYellow:      cssVar("--perch-warn",         "#fabd2f"),
      brightBlue:        cssVar("--perch-ansi-blue",    "#83a598"),
      brightMagenta:     cssVar("--perch-ansi-magenta", "#d3869b"),
      brightCyan:        cssVar("--perch-ansi-cyan",    "#8ec07c"),
      brightWhite:       cssVar("--perch-text",         "#ebdbb2"),
    };
  }

  /**
   * Fit the grid to the host and, if the dimensions actually changed, tell
   * the pty on the trailing edge, one SIGWINCH per settled resize. The
   * live-drag ResizeObserver and the become-visible effect both call this.
   * All the zero-dimension, hidden, and unchanged-grid guards live here, so
   * both callers are protected. This is a no-op after teardown.
   */
  function refit() {
    if (disposed || !term || !fit) return;
    fit.fit();
    const cols = Math.max(1, Math.min(PTY_MAX_DIM, term.cols | 0));
    const rows = Math.max(1, Math.min(PTY_MAX_DIM, term.rows | 0));
    if (!(Number.isFinite(cols) && Number.isFinite(rows) && cols > 0 && rows > 0)) return;
    // Nothing to tell the pty when the grid is unchanged from the last send.
    // A resize still pending for an intermediate size (A -> B -> A inside the
    // debounce window) is cancelled, or it would land while xterm is back at
    // A and leave the pty at B (FEX-14).
    if (cols === lastCols && rows === lastRows) {
      if (resizeTimer !== undefined) { clearTimeout(resizeTimer); resizeTimer = undefined; }
      return;
    }
    // Trailing edge: reset the timer on every changed frame, so a whole drag
    // collapses into one resizePty call when it settles.
    if (resizeTimer !== undefined) clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      resizeTimer = undefined;
      if (disposed) return;
      lastCols = cols;
      lastRows = rows;
      // A rejected resize (for example "unknown pane", when the first fit
      // lands before the backend registered the pty) is forgotten, so the
      // next fit, or resync(), sends the size again (FEX-15).
      resizePty(paneId, cols, rows).catch(() => {
        if (lastCols === cols && lastRows === rows) { lastCols = -1; lastRows = -1; }
      });
    }, PTY_RESIZE_DEBOUNCE_MS);
  }

  /** Re-send the current grid size to the pty. A parent calls this once the
      backend has the pty (after OpenShell or OpenWorkspace resolves), so a
      resize that raced the spawn is not lost (FEX-15). */
  export function resync(): void {
    lastCols = -1;
    lastRows = -1;
    refit();
  }

  /** Write a dim notice line into the terminal, for example a failed spawn
      (FEX-33). */
  export function notice(text: string): void {
    if (disposed || !term) return;
    term.write(`\r\n\x1b[2m${text}\x1b[0m\r\n`);
  }

  // Window-resize backstop. The host ResizeObserver fires only when the
  // .terminal box changes size, and under WebKitGTK a shrink or settle
  // notification can be coalesced or never delivered. This leaves an
  // oversized grid that xterm believes is fully visible, so it never scrolls
  // to the cursor during typing. A window 'resize' event catches those cases.
  // It coalesces through the same rafId guard the ResizeObserver uses, so the
  // two never double-schedule a fit within one frame.
  function onWindowResize() {
    if (rafId !== undefined) return;
    rafId = requestAnimationFrame(() => {
      rafId = undefined;
      refit();
    });
  }

  onMount(() => {
    term = new Terminal({
      convertEol: false,
      scrollback: TERMINAL_SCROLLBACK,
      fontFamily: cssVar("--perch-font-mono", "monospace"),
      fontSize:   parseInt(cssVar("--perch-fs-shell", "13px"), 10),
      lineHeight: parseFloat(cssVar("--perch-lh-shell", "1.5")),
      theme: buildXtermTheme(),
    });
    fit  = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    // Defer the first fit across a double rAF, mirroring the become-visible
    // effect. A synchronous fit() here runs before layout and cell metrics
    // settle, which can freeze the grid too tall, so xterm never scrolls to
    // the cursor during typing; a bare call also never told the pty.
    // Deferring measures a settled box and a non-zero cell metric, and sends
    // the first resizePty call through refit(). onDestroy cancels the
    // frames.
    initRaf1 = requestAnimationFrame(() => {
      initRaf2 = requestAnimationFrame(() => {
        if (!disposed) refit();
      });
    });

    offData = onPtyData(paneId, (bytes) => term.write(bytes));
    offExit = onPtyExit(paneId, (code) => {
      if (disposed) return;
      term.write(`\r\n\x1b[2m[process exited: ${code}]\x1b[0m\r\n`);
      onExit?.(code);
    });
    term.onData((d) => { writeToPty(paneId, new TextEncoder().encode(d)).catch(() => {}); });

    // Copy/paste: return false only for the two exact chords, so xterm
    // suppresses its default handling. Return true for everything else, so
    // ordinary keys, and bare ctrl-c (SIGINT/cancel), still reach the pty
    // untouched. This routes through the host clipboard (WebKit2GTK) because
    // navigator.clipboard is unreliable there.
    // The handler (lib/terminalKeys.ts) also hands app keys back to the
    // window keymap: every key, and every keypress, outside TERMINAL mode, and
    // the Ctrl-\ Ctrl-n leave sequence inside it (FEC-2).
    term.attachCustomKeyEventHandler(makeTerminalKeyHandler({
      hasSelection: () => term.hasSelection(),
      copy: () => { void clipboardSetText(term.getSelection()); },
      // term.paste routes through onData -> writeToPty and honors bracketed
      // paste mode, so shells and TUIs that opt in are protected.
      paste: () => { void clipboardText().then((t) => { if (t && !disposed) term.paste(t); }); },
    }));

    obs = new ResizeObserver(() => {
      // Coalesce fit() to one layout pass per frame. Many ticks can land
      // inside a single animation frame during a drag.
      if (rafId !== undefined) return;
      rafId = requestAnimationFrame(() => {
        rafId = undefined;
        refit();
      });
    });
    obs.observe(host);

    // Backstop for window shrinks that the host ResizeObserver may coalesce
    // or drop under WebKitGTK. Coalesced through the shared rafId guard
    // inside onWindowResize.
    window.addEventListener("resize", onWindowResize);

    // Right-click Copy/Paste menu. Attached here, not inline, to keep the
    // host div role-less. It is removed on teardown below.
    host.addEventListener("contextmenu", openTermMenu);

    // Re-apply the theme whenever the active theme changes, through the
    // data-theme attribute on <html>
    themeObs = new MutationObserver(() => {
      term.options.theme = buildXtermTheme();
    });
    themeObs.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  });

  /** Programmatic focus. Lets the parent route the keyboard to this pty
      without requiring a click; the awaiting-input auto-focus uses this.
      Safe to call before mount. */
  export function focus(): void { term?.focus(); }

  /** Send text to the pty as a paste. term.paste honours bracketed-paste
      mode, so a multi-line editor selection or hunk reaches the agent TUI as
      one paste instead of a series of Enter-terminated lines (FEX-28).
      Returns false when the terminal is not mounted, so the caller can fall
      back to a raw write. */
  export function paste(text: string): boolean {
    if (disposed || !term) return false;
    term.paste(text);
    return true;
  }

  // Re-fit on the hidden-to-visible edge. Reading `visible` first tracks it
  // as the sole dependency (term, fit, and disposed are plain lets,
  // deliberately untracked), so this re-runs only when the pane is shown,
  // never on unrelated state changes. The double rAF lets the browser apply
  // the ancestor display change and flush layout before FitAddon measures; a
  // single frame still reads the stale, zero-height box.
  $effect(() => {
    if (!visible || !term || !fit || disposed) return;
    let inner: number | undefined;
    const outer = requestAnimationFrame(() => {
      inner = requestAnimationFrame(() => {
        if (!disposed && visible) refit();
      });
    });
    // Cancel any still-pending frame on teardown or a visibility flip, so a
    // scheduled callback never fires after the component, or the test
    // environment, is gone.
    return () => {
      cancelAnimationFrame(outer);
      if (inner !== undefined) cancelAnimationFrame(inner);
    };
  });

  // ── Right-click Copy/Paste menu ───────────────────────────────────────────
  function openTermMenu(e: MouseEvent) {
    e.preventDefault();
    const x = Math.min(e.clientX, window.innerWidth  - MENU_APPROX_W);
    const y = Math.min(e.clientY, window.innerHeight - MENU_APPROX_H);
    menu = { x, y, canCopy: !!term?.hasSelection() };
  }
  function closeMenu() { menu = null; }
  function menuCopy() {
    if (term?.hasSelection()) void clipboardSetText(term.getSelection());
    closeMenu();
  }
  function menuPaste() {
    void clipboardText().then((t) => { if (t && !disposed) term?.paste(t); });
    closeMenu();
  }
  /** Keyboard support for the floating menu: activate on Enter or Space,
      close on Escape. */
  function handleMenuKey(e: KeyboardEvent, action: () => void) {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); action(); }
    else if (e.key === "Escape") { closeMenu(); }
  }
  /** Close the menu on Escape from anywhere while it is open. Window onclick
      handles an outside click. */
  function onWindowKey(e: KeyboardEvent) {
    if (menu && e.key === "Escape") closeMenu();
  }

  onDestroy(() => {
    disposed = true;
    if (rafId !== undefined) cancelAnimationFrame(rafId);
    if (initRaf1 !== undefined) cancelAnimationFrame(initRaf1);
    if (initRaf2 !== undefined) cancelAnimationFrame(initRaf2);
    if (resizeTimer !== undefined) clearTimeout(resizeTimer);
    window.removeEventListener("resize", onWindowResize);
    host?.removeEventListener("contextmenu", openTermMenu);
    offData?.();
    offExit?.();
    obs?.disconnect();
    themeObs?.disconnect();
    term?.dispose();
  });
</script>

<!-- The contextmenu listener is attached imperatively in onMount, not inline,
     so this xterm host stays a plain, role-less container. xterm owns its
     own accessibility through the textarea it renders inside. -->
<div class="terminal" bind:this={host}></div>

{#if menu}
  <ul role="menu" class="context-menu" style="position:fixed;left:{menu.x}px;top:{menu.y}px">
    <li role="menuitem" tabindex="0"
      class:disabled={!menu.canCopy}
      aria-disabled={menu.canCopy ? undefined : "true"}
      onclick={(e) => { e.stopPropagation(); if (menu?.canCopy) menuCopy(); }}
      onkeydown={(e) => { if (menu?.canCopy) handleMenuKey(e, menuCopy); else if (e.key === "Escape") closeMenu(); }}>Copy</li>
    <li role="menuitem" tabindex="0"
      onclick={(e) => { e.stopPropagation(); menuPaste(); }}
      onkeydown={(e) => handleMenuKey(e, menuPaste)}>Paste</li>
  </ul>
{/if}

<svelte:window onclick={closeMenu} onkeydown={onWindowKey} />

<style>
  .terminal {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    min-width: 0;
    /* xterm manages its own viewport. Do NOT set overflow here. */
  }

  /* ---------- Right-click Copy/Paste menu (mirrors FileTree) ---------- */
  .context-menu {
    list-style: none;
    margin: 0;
    padding: var(--perch-sp-1) 0;
    min-width: 140px;
    /* Solid, never glass. This menu overlaps the composited terminal subtree,
       where WebKitGTK paints backdrop-filter surfaces transparent (mirrors
       the fix in FileTree and ApprovalCard). */
    background: var(--perch-glass-bg-solid);
    border: 1px solid var(--perch-glass-border);
    border-radius: var(--perch-radius-md);
    box-shadow: var(--perch-shadow-float);
    z-index: var(--perch-z-context-menu);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }

  .context-menu [role="menuitem"] {
    display: flex;
    align-items: center;
    padding: calc(var(--perch-sp-1) * var(--perch-density-scale)) var(--perch-sp-2);
    color: var(--perch-text);
    cursor: pointer;
    transition: background var(--perch-dur) var(--perch-ease);
    user-select: none;
  }
  .context-menu [role="menuitem"]:not(.disabled):hover {
    background: color-mix(in srgb, var(--perch-accent) 10%, transparent);
  }
  .context-menu [role="menuitem"]:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: -2px;
  }
  .context-menu [role="menuitem"].disabled {
    color: var(--perch-text-dim);
    cursor: default;
    opacity: 0.5;
  }
</style>
