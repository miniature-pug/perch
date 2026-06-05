<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon }  from "@xterm/addon-fit";
  import { onPtyData, onPtyExit, writeToPty, resizePty } from "./wails";
  import { TERMINAL_SCROLLBACK, PTY_MAX_DIM } from "./constants";

  let { paneId, cwd, onExit }: { paneId: string; cwd: string; onExit?: (code: number) => void } = $props();

  // Hex-alpha suffix for the xterm text-selection layer. color-mix() isn't usable
  // as a raw ITheme value, so we append this to the accent hex instead. 0x66 ≈ 40%
  // opacity — enough to tint the selection without hiding the glyphs underneath.
  const SELECTION_ALPHA_HEX = "66";

  let host:     HTMLDivElement;
  let term:     Terminal;
  let fit:      FitAddon;
  let offData:  (() => void) | undefined;
  let offExit:  (() => void) | undefined;
  let obs:      ResizeObserver | undefined;
  let themeObs: MutationObserver | undefined;
  let disposed = false;

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

    obs = new ResizeObserver(() => {
      fit.fit();
      const cols = Math.max(1, Math.min(PTY_MAX_DIM, term.cols | 0));
      const rows = Math.max(1, Math.min(PTY_MAX_DIM, term.rows | 0));
      if (Number.isFinite(cols) && Number.isFinite(rows) && cols > 0 && rows > 0) {
        resizePty(paneId, cols, rows);
      }
    });
    obs.observe(host);

    // Re-apply theme whenever the active theme changes (data-theme attribute on <html>)
    themeObs = new MutationObserver(() => {
      term.options.theme = buildXtermTheme();
    });
    themeObs.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  });

  /** Programmatic focus — lets the parent route the keyboard to this pty without
      requiring a click (used by the awaiting-input auto-focus). Safe before mount. */
  export function focus(): void { term?.focus(); }

  onDestroy(() => {
    disposed = true;
    offData?.();
    offExit?.();
    obs?.disconnect();
    themeObs?.disconnect();
    term?.dispose();
  });
</script>

<div class="terminal" bind:this={host}></div>

<style>
  .terminal {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    min-width: 0;
    /* xterm manages its own viewport; do NOT set overflow here */
  }
</style>
