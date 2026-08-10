// Test hook shared between TerminalProbe and App.test.ts: a registry of the
// live panes' onExit callbacks, keyed by paneId, so a test can simulate an
// agent pty exit. There is no real xterm in jsdom to emit one. This lives in
// a .ts module, not the probe's `<script module>`, so `tsc --noEmit` sees
// the named export.
export const terminalExitHandlers: Record<string, (code: number) => void> = {};

// Companion registry: a per-paneId count of focus() calls on the probe. The
// real Terminal focuses its hidden xterm textarea, which jsdom cannot
// exercise, so App's `i`-key or awaiting-input focus (termRefs[id].focus())
// is otherwise unobservable. TerminalProbe.focus() increments this, so a
// test can assert the active pane was focused (F17).
export const terminalFocusCalls: Record<string, number> = {};

// Companion registry: a per-paneId count of Terminal component mounts. Each
// real Terminal builds a fresh xterm on mount, so a mount means a blank
// buffer. This increments once per component-instance mount, never on a DOM
// relocation, which reuses the instance. A test asserts the split session's
// terminal is mounted exactly once across a split off-on-off toggle. This
// proves its node, and its xterm buffer, is relocated, not destroyed and
// recreated (F10a).
export const terminalMountCounts: Record<string, number> = {};
