import { render, cleanup } from "@testing-library/svelte";
import { vi, expect } from "vitest";

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

// Default: openTerminal resolves immediately.
// Individual tests may override with mockImplementationOnce for a deferred promise.
vi.mock("./wails", () => ({
  onPtyData: vi.fn((_t: string, cb: (b: Uint8Array) => void) => { ptyCbs.push(cb); return () => {}; }),
  openTerminal: vi.fn(async () => {}),
  writeToPty: vi.fn(async () => {}),
  resizePty: vi.fn(async () => {}),
  closeTerminal: vi.fn(async () => {}),
}));

const ptyCbs: Array<(b: Uint8Array) => void> = [];

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

test("reaps bridge if component is destroyed while openTerminal is in-flight", async () => {
  const { default: Terminal } = await import("./Terminal.svelte");
  const w = await import("./wails");
  vi.mocked(w.closeTerminal).mockClear();

  // Make openTerminal return a promise we control.
  let resolveOpen!: () => void;
  vi.mocked(w.openTerminal).mockImplementationOnce(
    () => new Promise<void>((res) => { resolveOpen = res; }),
  );

  const { unmount } = render(Terminal, { props: { tabId: "tab-deferred", sessionId: "ses_b" } });

  // Destroy the component BEFORE the async openTerminal resolves.
  unmount();

  // After unmount, onDestroy has fired: closeTerminal called once (fire-and-forget).
  expect(vi.mocked(w.closeTerminal)).toHaveBeenCalledTimes(1);

  // Now resolve openTerminal — the backend would register the bridge at this point.
  // The destroyed-guard must detect this and reap the bridge (second call).
  resolveOpen();
  // Flush microtasks so the post-await continuation runs.
  await new Promise<void>((res) => setTimeout(res, 0));

  expect(vi.mocked(w.closeTerminal)).toHaveBeenCalledTimes(2);
  expect(vi.mocked(w.closeTerminal)).toHaveBeenCalledWith("tab-deferred");
  // resizePty must NOT have been called (we returned early).
  expect(vi.mocked(w.resizePty)).not.toHaveBeenCalledWith("tab-deferred", expect.anything(), expect.anything());

  // Restore default immediate mock for subsequent tests.
  vi.mocked(w.openTerminal).mockImplementation(async () => {});
});
