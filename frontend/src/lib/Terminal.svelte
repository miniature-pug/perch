<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon }  from "@xterm/addon-fit";
  import { onPtyData, onPtyExit, writeToPty, resizePty, clipboardSetText, clipboardText } from "./wails";
  import { TERMINAL_SCROLLBACK, PTY_MAX_DIM } from "./constants";

  // `visible` mirrors the pattern Editor/FileTree/Preview already use: the pane is
  // kept MOUNTED and merely hidden by an ancestor `display:none` on collapse/tab
  // switch. A ResizeObserver never fires for an ancestor display toggle, so without
  // this signal the terminal's cols/rows go stale while hidden and the cursor
  // scrolls out of view on re-show. The effect below re-fits on the hidden→visible
  // edge to correct that.
  let { paneId, cwd, onExit, visible = true }:
    { paneId: string; cwd: string; onExit?: (code: number) => void; visible?: boolean } = $props();

  // Hex-alpha suffix for the xterm text-selection layer. color-mix() isn't usable
  // as a raw ITheme value, so we append this to the accent hex instead. 0x66 ≈ 40%
  // opacity — enough to tint the selection without hiding the glyphs underneath.
  const SELECTION_ALPHA_HEX = "66";

  // Trailing-edge debounce for the pty resize. A splitter drag fires the
  // ResizeObserver dozens of times per second; each resizePty is a SIGWINCH the
  // agent TUI reflows on, so a raw storm makes it splutter. We coalesce fit() into
  // one layout pass per animation frame and only send the resize once the drag
  // settles, and only when the dimensions actually changed. 80ms spans a drag's
  // frame cadence yet still feels instant on release.
  const PTY_RESIZE_DEBOUNCE_MS = 80;

  let host:     HTMLDivElement;
  let term:     Terminal;
  let fit:      FitAddon;
  let offData:  (() => void) | undefined;
  let offExit:  (() => void) | undefined;
  let obs:      ResizeObserver | undefined;
  let themeObs: MutationObserver | undefined;
  let disposed = false;

  // Resize coalescing state: one pending rAF for fit(), one trailing-edge timer
  // for the pty resize, and the last cols/rows we actually sent (so an unchanged
  // observation is a no-op). -1 is an impossible dimension, so the first real
  // measurement always sends.
  let rafId:       number | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let lastCols = -1;
  let lastRows = -1;

  // Right-click Copy/Paste menu (modelled on FileTree's context menu). `canCopy`
  // is snapshotted at open time from term.hasSelection() so the Copy item's
  // enabled state is stable while the menu is up. Approx dims clamp the menu
  // inside the viewport, same as FileTree.
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
      // Standard 16-colour ANSI mapped to Gruvbox equivalents via tokens where possible
      black:             cssVar("--perch-bg-elev",      "#1d2021"),
      red:               cssVar("--perch-err",          "#fb4934"),
      green:             cssVar("--perch-ok",           "#b8bb26"),
      yellow:            cssVar("--perch-warn",         "#fabd2f"),
      blue:              cssVar("--perch-info",         "#83a598"),
      magenta:           cssVar("--perch-accent",       "#d79921"),
      cyan:              cssVar("--perch-info",         "#83a598"),
      white:             cssVar("--perch-text",         "#ebdbb2"),
      brightBlack:       cssVar("--perch-text-dim",     "#a89984"),
      brightRed:         cssVar("--perch-err",          "#fb4934"),
      brightGreen:       cssVar("--perch-ok",           "#b8bb26"),
      brightYellow:      cssVar("--perch-warn",         "#fabd2f"),
      brightBlue:        cssVar("--perch-info",         "#83a598"),
      brightMagenta:     cssVar("--perch-accent",       "#d79921"),
      brightCyan:        cssVar("--perch-info",         "#83a598"),
      brightWhite:       cssVar("--perch-text",         "#ebdbb2"),
    };
  }

  /**
   * Fit the grid to the host and, if the dimensions actually changed, tell the
   * pty on the trailing edge (one SIGWINCH per settled resize). Shared by the
   * live-drag ResizeObserver and the become-visible effect. All the 0-dimension /
   * hidden / unchanged-grid guards live here, so both callers are protected. No-op
   * after teardown.
   */
  function refit() {
    if (disposed || !term || !fit) return;
    fit.fit();
    const cols = Math.max(1, Math.min(PTY_MAX_DIM, term.cols | 0));
    const rows = Math.max(1, Math.min(PTY_MAX_DIM, term.rows | 0));
    if (!(Number.isFinite(cols) && Number.isFinite(rows) && cols > 0 && rows > 0)) return;
    // Nothing to tell the pty if the grid is unchanged from the last send.
    if (cols === lastCols && rows === lastRows) return;
    // Trailing edge: reset the timer on every changed frame so a whole drag
    // collapses to one resizePty when it settles.
    if (resizeTimer !== undefined) clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      resizeTimer = undefined;
      if (disposed) return;
      lastCols = cols;
      lastRows = rows;
      resizePty(paneId, cols, rows);
    }, PTY_RESIZE_DEBOUNCE_MS);
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
    fit.fit();

    offData = onPtyData(paneId, (bytes) => term.write(bytes));
    offExit = onPtyExit(paneId, (code) => {
      if (disposed) return;
      term.write(`\r\n\x1b[2m[process exited: ${code}]\x1b[0m\r\n`);
      onExit?.(code);
    });
    term.onData((d) => writeToPty(paneId, Array.from(new TextEncoder().encode(d))));

    // Copy/paste: return FALSE only for the two exact chords so xterm suppresses
    // its default handling; TRUE for everything else so ordinary keys — and bare
    // ctrl-c (SIGINT/cancel) — still reach the pty untouched. We route through the
    // host clipboard (WebKit2GTK) because navigator.clipboard is unreliable there.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown") return true;
      const chord = e.ctrlKey && e.shiftKey;
      if (chord && (e.key === "C" || e.key === "c") && term.hasSelection()) {
        void clipboardSetText(term.getSelection());
        return false;
      }
      if (chord && (e.key === "V" || e.key === "v")) {
        // term.paste routes through onData -> writeToPty and honours bracketed-paste
        // mode, so shells/TUIs that opt in are protected — no manual encoding here.
        void clipboardText().then((t) => { if (t && !disposed) term.paste(t); });
        return false;
      }
      return true;
    });

    obs = new ResizeObserver(() => {
      // Coalesce fit() to one layout pass per frame — many ticks can land inside
      // a single animation frame during a drag.
      if (rafId !== undefined) return;
      rafId = requestAnimationFrame(() => {
        rafId = undefined;
        refit();
      });
    });
    obs.observe(host);

    // Right-click Copy/Paste menu. Attached here (not inline) to keep the host div
    // role-less; removed on teardown below.
    host.addEventListener("contextmenu", openTermMenu);

    // Re-apply theme whenever the active theme changes (data-theme attribute on <html>)
    themeObs = new MutationObserver(() => {
      term.options.theme = buildXtermTheme();
    });
    themeObs.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  });

  /** Programmatic focus — lets the parent route the keyboard to this pty without
      requiring a click (used by the awaiting-input auto-focus). Safe before mount. */
  export function focus(): void { term?.focus(); }

  // Re-fit on the hidden→visible edge. Reading `visible` first tracks it as the
  // sole dependency (term/fit/disposed are plain lets, deliberately untracked), so
  // this re-runs only when the pane is shown, never on unrelated state churn. The
  // double rAF lets the browser apply the ancestor display change and flush layout
  // before FitAddon measures — a single frame still reads the stale (0-height) box.
  $effect(() => {
    if (!visible || !term || !fit || disposed) return;
    let inner: number | undefined;
    const outer = requestAnimationFrame(() => {
      inner = requestAnimationFrame(() => {
        if (!disposed && visible) refit();
      });
    });
    // Cancel any still-pending frame on teardown / visibility flip, so a scheduled
    // callback never fires after the component (or the test environment) is gone.
    return () => {
      cancelAnimationFrame(outer);
      if (inner !== undefined) cancelAnimationFrame(inner);
    };
  });

  // ── Right-click Copy/Paste menu ──────────────────────────────────────────────
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
  /** Keyboard support for the floating menu: activate on Enter/Space, close on Escape. */
  function handleMenuKey(e: KeyboardEvent, action: () => void) {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); action(); }
    else if (e.key === "Escape") { closeMenu(); }
  }
  /** Close the menu on Escape from anywhere while it is open (outside-click is handled by window onclick). */
  function onWindowKey(e: KeyboardEvent) {
    if (menu && e.key === "Escape") closeMenu();
  }

  onDestroy(() => {
    disposed = true;
    if (rafId !== undefined) cancelAnimationFrame(rafId);
    if (resizeTimer !== undefined) clearTimeout(resizeTimer);
    host?.removeEventListener("contextmenu", openTermMenu);
    offData?.();
    offExit?.();
    obs?.disconnect();
    themeObs?.disconnect();
    term?.dispose();
  });
</script>

<!-- The contextmenu listener is attached imperatively in onMount (not inline) so
     this xterm host stays a plain, role-less container — xterm owns its own a11y
     via the textarea it renders inside. -->
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
    /* xterm manages its own viewport; do NOT set overflow here */
  }

  /* ---------- Right-click Copy/Paste menu (mirrors FileTree) ---------- */
  .context-menu {
    list-style: none;
    margin: 0;
    padding: var(--perch-sp-1) 0;
    min-width: 140px;
    /* Solid, never glass: this menu overlaps the composited terminal subtree,
       where WebKitGTK paints backdrop-filter surfaces transparent (mirrors the
       FileTree/ApprovalCard fix). */
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
