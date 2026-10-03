// frontend/src/lib/terminalKeys.ts
//
// Which keystrokes an xterm passes to its pty, and which it hands back to the
// app keymap. xterm handles keys on its own textarea in keydown and stops
// propagation, so the window keymap in App.svelte never sees a key xterm
// accepted. Terminal.svelte asks this module from xterm's custom key handler:
// returning false there makes xterm ignore the event, which then bubbles to
// the window keymap (FEC-2).
//
// - Outside TERMINAL mode the terminal is passive: every key belongs to the
//   app, so NORMAL-mode shortcuts work even while an xterm holds focus, and
//   nothing typed there reaches the agent.
// - In TERMINAL mode every key goes to the pty except the leave sequence,
//   Ctrl-\ then Ctrl-n, which the app consumes. A modifier press alone
//   (releasing and re-pressing Ctrl) keeps the sequence armed; any other key
//   that goes to the pty disarms it.

import { mode } from "./stores/mode.svelte";

export const MODIFIER_KEYS = new Set(["Shift", "Control", "Alt", "Meta", "AltGraph", "CapsLock"]);

export function isModifierKey(e: { key: string }): boolean {
  return MODIFIER_KEYS.has(e.key);
}

/** True for Ctrl-\, the first key of the TERMINAL-mode leave sequence. */
export function isLeavePrefix(e: KeyboardEvent): boolean {
  return e.ctrlKey && !e.altKey && !e.metaKey && (e.key === "\\" || e.code === "Backslash");
}

/** True for Ctrl-n, the second key of the leave sequence. */
export function isLeaveFinal(e: KeyboardEvent): boolean {
  return e.ctrlKey && !e.altKey && !e.metaKey && (e.key === "n" || e.key === "N");
}

/**
 * Decide whether a key event on an xterm goes to the pty (true) or to the app
 * keymap (false). The return value matches xterm's custom key handler
 * contract.
 */
export function terminalKeyToPty(e: KeyboardEvent): boolean {
  if (mode.current !== "terminal") return false;
  if (e.type !== "keydown") return true;
  if (isLeavePrefix(e)) return false;
  if (mode.leavePending && isLeaveFinal(e)) return false;
  if (!isModifierKey(e)) mode.leavePending = false;
  return true;
}

/**
 * The full xterm custom key handler: the host-clipboard copy/paste chords
 * (Ctrl-Shift-C with a selection, Ctrl-Shift-V), then terminalKeyToPty for
 * everything else. Every event type goes through terminalKeyToPty, keypress
 * included: xterm sends printable characters from keypress, so letting a
 * keypress through in NORMAL mode would type into the agent (review #1).
 */
export function makeTerminalKeyHandler(clip: {
  hasSelection: () => boolean;
  copy: () => void;
  paste: () => void;
}): (e: KeyboardEvent) => boolean {
  return (e) => {
    if (e.type === "keydown") {
      const chord = e.ctrlKey && e.shiftKey;
      if (chord && (e.key === "C" || e.key === "c") && clip.hasSelection()) {
        clip.copy();
        return false;
      }
      if (chord && (e.key === "V" || e.key === "v")) {
        clip.paste();
        return false;
      }
    }
    return terminalKeyToPty(e);
  };
}
