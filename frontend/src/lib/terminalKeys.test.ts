// frontend/src/lib/terminalKeys.test.ts
//
// FEC-2 regression against a REAL xterm in jsdom. xterm handles keys on its
// textarea in keydown and stops propagation, so a bubble-phase window keymap
// never saw Ctrl-\ or the NORMAL-mode shortcuts, and the pty received them
// instead. terminalKeyToPty, installed as xterm's custom key handler, hands
// those keys back.
import { Terminal } from "@xterm/xterm";
import { afterEach, beforeAll, expect, test } from "vitest";
import { terminalKeyToPty } from "./terminalKeys";
import { mode } from "./stores/mode.svelte";

beforeAll(() => {
  (window as any).matchMedia ??= () => ({
    matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
  });
});

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; mode.leaveTerminal(); });

function mountXterm() {
  const host = document.createElement("div");
  document.body.appendChild(host);
  const term = new Terminal();
  term.open(host);
  term.attachCustomKeyEventHandler(terminalKeyToPty);
  const pty: string[] = [];
  term.onData((d) => pty.push(d));
  const seen: string[] = [];
  const onKey = (e: KeyboardEvent) => seen.push((e.ctrlKey ? "C-" : "") + e.key);
  window.addEventListener("keydown", onKey);
  cleanup = () => { window.removeEventListener("keydown", onKey); term.dispose(); host.remove(); };
  const ta = host.querySelector("textarea")!;
  const press = (key: string, keyCode: number, ctrlKey = false) =>
    ta.dispatchEvent(new KeyboardEvent("keydown", { key, keyCode, ctrlKey, bubbles: true, cancelable: true } as KeyboardEventInit));
  return { pty, seen, press };
}

const BACKSLASH = String.fromCharCode(92);

test("FEC-2: in TERMINAL mode the window keymap sees Ctrl-backslash and the pty does not", () => {
  mode.enterTerminal();
  const { pty, seen, press } = mountXterm();
  press(BACKSLASH, 220, true);
  expect(seen).toContain("C-" + BACKSLASH);
  expect(pty).not.toContain(String.fromCharCode(0x1c));
  // Ordinary typing still reaches the pty.
  press("j", 74);
  expect(pty).toContain("j");
});

test("FEC-2: in NORMAL mode shortcuts reach the window keymap and nothing reaches the pty", () => {
  mode.leaveTerminal();
  const { pty, seen, press } = mountXterm();
  press("j", 74);
  press("1", 49);
  expect(seen).toEqual(["j", "1"]);
  expect(pty).toEqual([]);
});
