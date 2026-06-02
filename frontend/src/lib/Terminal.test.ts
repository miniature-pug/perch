import { render } from "@testing-library/svelte";
import { vi } from "vitest";

const writeSpy = vi.fn();
const onDataCbs: Array<(d: string) => void> = [];
vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open() {}
    write(d: Uint8Array | string) { writeSpy(d); }
    onData(cb: (d: string) => void) { onDataCbs.push(cb); return { dispose() {} }; }
    loadAddon() {}
    get cols() { return 80; }
    get rows() { return 24; }
    dispose() {}
  },
}));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() {} } }));

const ptyCbs: Array<(b: Uint8Array) => void> = [];
vi.mock("./wails", () => ({
  onPtyData: vi.fn((_t: string, cb: (b: Uint8Array) => void) => { ptyCbs.push(cb); return () => {}; }),
  openTerminal: vi.fn(async () => {}),
  writeToPty: vi.fn(async () => {}),
  resizePty: vi.fn(async () => {}),
  closeTerminal: vi.fn(async () => {}),
}));

test("opens the terminal and writes incoming pty bytes to xterm", async () => {
  const { default: Terminal } = await import("./Terminal.svelte");
  const w = await import("./wails");
  render(Terminal, { props: { tabId: "tab1", sessionId: "ses_a" } });
  expect(w.openTerminal).toHaveBeenCalledWith("tab1", "ses_a");
  ptyCbs[0](Uint8Array.from([104, 105])); // "hi"
  expect(writeSpy).toHaveBeenCalled();
});

test("forwards keystrokes to the backend", async () => {
  const { default: Terminal } = await import("./Terminal.svelte");
  const w = await import("./wails");
  render(Terminal, { props: { tabId: "tab2", sessionId: "ses_a" } });
  onDataCbs[onDataCbs.length - 1]("x");
  expect(w.writeToPty).toHaveBeenCalledWith("tab2", [120]);
});
