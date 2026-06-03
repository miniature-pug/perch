// frontend/src/lib/Terminal.test.ts
import { render, cleanup } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

const writeSpy   = vi.fn();
const disposeSpy = vi.fn();
const onDataCbs: Array<(d: string) => void> = [];

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open(_el: HTMLElement) {}
    write(d: Uint8Array | string) { writeSpy(d); }
    onData(cb: (d: string) => void) { onDataCbs.push(cb); return { dispose() {} }; }
    loadAddon() {}
    get cols() { return 80; }
    get rows() { return 24; }
    dispose() { disposeSpy(); }
  },
}));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() {} } }));

beforeEach(() => {
  (globalThis as any).ResizeObserver = class {
    observe()    {}
    unobserve()  {}
    disconnect() {}
  };
});

const ptyCbs: Array<(b: Uint8Array) => void> = [];
const exitCbs: Array<(code: number) => void> = [];

vi.mock("./wails", () => ({
  onPtyData:  vi.fn((_id: string, cb: (b: Uint8Array) => void) => { ptyCbs.push(cb); return () => {}; }),
  onPtyExit:  vi.fn((_id: string, cb: (code: number) => void) => { exitCbs.push(cb); return () => {}; }),
  writeToPty: vi.fn(async () => {}),
  resizePty:  vi.fn(async () => {}),
}));

afterEach(() => {
  cleanup(); writeSpy.mockClear(); disposeSpy.mockClear();
  ptyCbs.length = 0; onDataCbs.length = 0; exitCbs.length = 0;
});

describe("Terminal.svelte", () => {
  it("subscribes to pty:data:<paneId> on mount", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "pane1", cwd: "/repo" } });
    expect(w.onPtyData).toHaveBeenCalledWith("pane1", expect.any(Function));
  });
  it("writes incoming bytes to xterm", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    render(Terminal, { props: { paneId: "pane2", cwd: "/repo" } });
    ptyCbs[ptyCbs.length - 1](Uint8Array.from([104, 105]));
    expect(writeSpy).toHaveBeenCalledWith(Uint8Array.from([104, 105]));
  });
  it("forwards keystrokes via writeToPty", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "pane3", cwd: "/repo" } });
    onDataCbs[onDataCbs.length - 1]("x");
    expect(w.writeToPty).toHaveBeenCalledWith("pane3", [120]);
  });
  it("disposes xterm on unmount (leak guard)", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const { unmount } = render(Terminal, { props: { paneId: "pane4", cwd: "/repo" } });
    unmount();
    expect(disposeSpy).toHaveBeenCalled();
  });
  it("calls the unsubscribe fn returned by onPtyData on unmount", async () => {
    const offSpy = vi.fn();
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    vi.mocked(w.onPtyData).mockReturnValueOnce(offSpy);
    const { unmount } = render(Terminal, { props: { paneId: "pane5", cwd: "/repo" } });
    unmount();
    expect(offSpy).toHaveBeenCalled();
  });
  it("subscribes to pty:exit:<paneId> on mount", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "pane6", cwd: "/repo" } });
    expect(w.onPtyExit).toHaveBeenCalledWith("pane6", expect.any(Function));
  });
  it("writes an exit notice and invokes onExit when the process exits", async () => {
    const onExit = vi.fn();
    const { default: Terminal } = await import("./Terminal.svelte");
    render(Terminal, { props: { paneId: "pane7", cwd: "/repo", onExit } });
    exitCbs[exitCbs.length - 1](7);
    expect(onExit).toHaveBeenCalledWith(7);
    expect(writeSpy).toHaveBeenCalledWith(expect.stringContaining("exited"));
  });
  it("calls the unsubscribe fn returned by onPtyExit on unmount", async () => {
    const offSpy = vi.fn();
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    vi.mocked(w.onPtyExit).mockReturnValueOnce(offSpy);
    const { unmount } = render(Terminal, { props: { paneId: "pane8", cwd: "/repo" } });
    unmount();
    expect(offSpy).toHaveBeenCalled();
  });
});
