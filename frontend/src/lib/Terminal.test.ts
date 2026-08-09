// frontend/src/lib/Terminal.test.ts
import { render, cleanup } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

const writeSpy   = vi.fn();
const disposeSpy = vi.fn();
const focusSpy   = vi.fn();
const onDataCbs: Array<(d: string) => void> = [];

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open(_el: HTMLElement) {}
    write(d: Uint8Array | string) { writeSpy(d); }
    onData(cb: (d: string) => void) { onDataCbs.push(cb); return { dispose() {} }; }
    loadAddon() {}
    focus() { focusSpy(); }
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

const realRAF = globalThis.requestAnimationFrame;

afterEach(() => {
  cleanup(); writeSpy.mockClear(); disposeSpy.mockClear(); focusSpy.mockClear();
  ptyCbs.length = 0; onDataCbs.length = 0; exitCbs.length = 0;
  globalThis.requestAnimationFrame = realRAF;
  vi.useRealTimers();
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
  it("collapses a resize storm into a single pty resize (rAF fit + trailing-edge dedup)", async () => {
    // Capture the ResizeObserver callback so we can fire ticks like a drag would.
    let roCb: () => void = () => {};
    (globalThis as any).ResizeObserver = class {
      constructor(fn: () => void) { roCb = fn; }
      observe()    {}
      unobserve()  {}
      disconnect() {}
    };
    // Queue rAF callbacks instead of running them synchronously, so the per-frame
    // fit() coalescing is exercised the same way a real animation frame would.
    const rafQueue: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((fn: FrameRequestCallback) => rafQueue.push(fn)) as any;
    const flushRaf = () => { rafQueue.splice(0).forEach((fn) => fn(0)); };

    vi.useFakeTimers();

    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "paneR", cwd: "/repo" } });
    vi.mocked(w.resizePty).mockClear();

    // 10 frames, 2 ticks each — 20 observations, all reporting the mock's 80x24.
    for (let frame = 0; frame < 10; frame++) {
      roCb(); roCb();
      flushRaf();
    }
    // Nothing sent yet: the trailing edge has not elapsed.
    expect(w.resizePty).not.toHaveBeenCalled();

    vi.advanceTimersByTime(200);
    expect(w.resizePty).toHaveBeenCalledTimes(1);
    expect(w.resizePty).toHaveBeenCalledWith("paneR", 80, 24);

    // Further ticks at the same size must not re-send (dedup on unchanged grid).
    for (let i = 0; i < 5; i++) { roCb(); flushRaf(); }
    vi.advanceTimersByTime(200);
    expect(w.resizePty).toHaveBeenCalledTimes(1);
  });

  it("exported focus() forwards to the underlying xterm (awaiting-input auto-focus mechanism)", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const { component } = render(Terminal, { props: { paneId: "pane9", cwd: "/repo" } });
    (component as unknown as { focus: () => void }).focus();
    expect(focusSpy).toHaveBeenCalled();
  });
});
