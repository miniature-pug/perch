// frontend/src/lib/Terminal.test.ts
import { render, cleanup, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

const writeSpy   = vi.fn();
const disposeSpy = vi.fn();
const focusSpy   = vi.fn();
const pasteSpy   = vi.fn();
const fitSpy     = vi.fn();
const onDataCbs: Array<(d: string) => void> = [];
// This captures the attachCustomKeyEventHandler callback and a controllable selection.
// The copy/paste chord and context-menu tests can then drive the real handler directly.
let keyHandler: ((e: KeyboardEvent) => boolean) | null = null;
let selectionText = "";
// These are the grid dimensions the mock reports. The default is 80x24, which every
// existing test pins. A test can change the dimensions mid-run to prove a resize actually re-sends the new dimensions.
let mockCols = 80;
let mockRows = 24;

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open(_el: HTMLElement) {}
    write(d: Uint8Array | string) { writeSpy(d); }
    onData(cb: (d: string) => void) { onDataCbs.push(cb); return { dispose() {} }; }
    loadAddon() {}
    focus() { focusSpy(); }
    attachCustomKeyEventHandler(fn: (e: KeyboardEvent) => boolean) { keyHandler = fn; }
    hasSelection() { return selectionText.length > 0; }
    getSelection() { return selectionText; }
    paste(t: string) { pasteSpy(t); }
    get cols() { return mockCols; }
    get rows() { return mockRows; }
    dispose() { disposeSpy(); }
  },
}));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() { fitSpy(); } } }));

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
  clipboardSetText: vi.fn(async () => {}),
  clipboardText:    vi.fn(async () => ""),
}));

const realRAF = globalThis.requestAnimationFrame;
const realCAF = globalThis.cancelAnimationFrame;

afterEach(async () => {
  cleanup();
  // First restore vi's fake timers. Then force the known-good jsdom rAF and CAF back.
  // The next test's mount effect schedules a frame, so that effect always needs a working global.
  vi.useRealTimers();
  globalThis.requestAnimationFrame = realRAF;
  globalThis.cancelAnimationFrame = realCAF;
  writeSpy.mockClear(); disposeSpy.mockClear(); focusSpy.mockClear();
  pasteSpy.mockClear(); fitSpy.mockClear();
  ptyCbs.length = 0; onDataCbs.length = 0; exitCbs.length = 0;
  keyHandler = null; selectionText = ""; mockCols = 80; mockRows = 24;
  const w = await import("./wails");
  vi.mocked(w.clipboardSetText).mockClear();
  vi.mocked(w.clipboardText).mockClear();
  vi.mocked(w.writeToPty).mockClear();
  vi.mocked(w.resizePty).mockClear();
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
    // This captures the ResizeObserver callback, so the test can fire ticks like a drag would.
    let roCb: () => void = () => {};
    (globalThis as any).ResizeObserver = class {
      constructor(fn: () => void) { roCb = fn; }
      observe()    {}
      unobserve()  {}
      disconnect() {}
    };
    // This queues rAF callbacks instead of running them synchronously. The test then
    // exercises the per-frame fit() coalescing the same way a real animation frame would.
    const rafQueue: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((fn: FrameRequestCallback) => rafQueue.push(fn)) as any;
    const flushRaf = () => { rafQueue.splice(0).forEach((fn) => fn(0)); };

    vi.useFakeTimers();

    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "paneR", cwd: "/repo" } });
    vi.mocked(w.resizePty).mockClear();

    // 10 frames, 2 ticks each: 20 observations total, all reporting the mock's 80x24.
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

  // ── Copy/paste chords (attachCustomKeyEventHandler) ─────────────────────────
  it("ctrl+shift+c with a selection copies it host-side and swallows the chord", async () => {
    selectionText = "hello world";
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "paneC", cwd: "/repo" } });
    const ret = keyHandler!({ type: "keydown", ctrlKey: true, shiftKey: true, key: "C" } as unknown as KeyboardEvent);
    expect(ret).toBe(false);
    expect(w.clipboardSetText).toHaveBeenCalledWith("hello world");
  });

  it("ctrl+shift+v pastes the host clipboard text and swallows the chord", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    vi.mocked(w.clipboardText).mockResolvedValueOnce("pasted!");
    render(Terminal, { props: { paneId: "paneV", cwd: "/repo" } });
    const ret = keyHandler!({ type: "keydown", ctrlKey: true, shiftKey: true, key: "v" } as unknown as KeyboardEvent);
    expect(ret).toBe(false);
    await waitFor(() => expect(pasteSpy).toHaveBeenCalledWith("pasted!"));
  });

  it("passes ordinary keys and bare ctrl-c through to the pty (handler returns true)", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    render(Terminal, { props: { paneId: "paneK", cwd: "/repo" } });
    // A plain key must reach the pty.
    expect(keyHandler!({ type: "keydown", ctrlKey: false, shiftKey: false, key: "a" } as unknown as KeyboardEvent)).toBe(true);
    // The copy chord must NOT swallow bare ctrl-c (SIGINT/cancel).
    expect(keyHandler!({ type: "keydown", ctrlKey: true, shiftKey: false, key: "c" } as unknown as KeyboardEvent)).toBe(true);
    // ctrl+shift+c with no selection is not a copy. The handler passes it through.
    selectionText = "";
    expect(keyHandler!({ type: "keydown", ctrlKey: true, shiftKey: true, key: "c" } as unknown as KeyboardEvent)).toBe(true);
  });

  // ── Right-click context menu ────────────────────────────────────────────────
  it("right-click opens the Copy/Paste menu; Copy is disabled without a selection", async () => {
    selectionText = "";
    const { default: Terminal } = await import("./Terminal.svelte");
    const { container } = render(Terminal, { props: { paneId: "paneM", cwd: "/repo" } });
    await fireEvent.contextMenu(container.querySelector(".terminal")!);
    expect(container.querySelector('[role="menu"]')).not.toBeNull();
    const copy = screen.getByRole("menuitem", { name: /copy/i });
    expect(copy.getAttribute("aria-disabled")).toBe("true");
    // The disabled Copy is inert.
    const w = await import("./wails");
    await fireEvent.click(copy);
    expect(w.clipboardSetText).not.toHaveBeenCalled();
  });

  it("context-menu Copy is enabled with a selection and copies it host-side", async () => {
    selectionText = "selected text";
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    const { container } = render(Terminal, { props: { paneId: "paneM2", cwd: "/repo" } });
    await fireEvent.contextMenu(container.querySelector(".terminal")!);
    const copy = screen.getByRole("menuitem", { name: /copy/i });
    expect(copy.getAttribute("aria-disabled")).not.toBe("true");
    await fireEvent.click(copy);
    expect(w.clipboardSetText).toHaveBeenCalledWith("selected text");
    // Menu closes after the action.
    expect(container.querySelector('[role="menu"]')).toBeNull();
  });

  it("context-menu Paste pastes the host clipboard text", async () => {
    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    vi.mocked(w.clipboardText).mockResolvedValueOnce("clip");
    const { container } = render(Terminal, { props: { paneId: "paneM3", cwd: "/repo" } });
    await fireEvent.contextMenu(container.querySelector(".terminal")!);
    await fireEvent.click(screen.getByRole("menuitem", { name: /paste/i }));
    await waitFor(() => expect(pasteSpy).toHaveBeenCalledWith("clip"));
  });

  // ── Re-fit when the pane becomes visible (A2) ───────────────────────────────
  it("re-fits when it becomes visible again (double-rAF -> fit + resizePty)", async () => {
    // vi.useFakeTimers() first installs its own fake requestAnimationFrame. The test then
    // overrides rAF with a manual queue after that call, to keep control of the frames.
    vi.useFakeTimers();
    const rafQueue: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((fn: FrameRequestCallback) => rafQueue.push(fn)) as any;
    const flushRaf = () => { rafQueue.splice(0).forEach((fn) => fn(0)); };

    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    const { rerender } = render(Terminal, { props: { paneId: "paneVis", cwd: "/repo", visible: false } });
    // This ignores any fit() or resize scheduled during mount. The test cares only about the show transition.
    fitSpy.mockClear();
    vi.mocked(w.resizePty).mockClear();

    // Flip the state from hidden to visible: the effect schedules a double rAF, then a debounced resize.
    await rerender({ props: { paneId: "paneVis", cwd: "/repo", visible: true } });
    flushRaf(); // The outer rAF then schedules the inner one.
    flushRaf(); // The inner rAF then calls refit().
    expect(fitSpy).toHaveBeenCalled();

    vi.advanceTimersByTime(200);
    expect(w.resizePty).toHaveBeenCalledWith("paneVis", 80, 24);
  });

  it("does not re-fit while it stays hidden (visible=false is a no-op)", async () => {
    vi.useFakeTimers();
    const rafQueue: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((fn: FrameRequestCallback) => rafQueue.push(fn)) as any;
    const flushRaf = () => { rafQueue.splice(0).forEach((fn) => fn(0)); };

    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "paneHidden", cwd: "/repo", visible: false } });
    fitSpy.mockClear();
    vi.mocked(w.resizePty).mockClear();

    flushRaf();
    vi.advanceTimersByTime(200);
    expect(w.resizePty).not.toHaveBeenCalled();
  });

  // ── Initial fit is deferred across a double rAF (A1) ─────────────────────────
  it("defers the initial fit to a double rAF and sends the first resize (was a bare synchronous no-op)", async () => {
    vi.useFakeTimers();
    const rafQueue: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((fn: FrameRequestCallback) => rafQueue.push(fn)) as any;
    const flushRaf = () => { rafQueue.splice(0).forEach((fn) => fn(0)); };

    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "paneInit", cwd: "/repo" } });

    // Deferred: the code does not fit synchronously at mount time. (The old code ran a
    // bare fit.fit() here before layout and cell metrics settled, and it never resized.)
    expect(fitSpy).not.toHaveBeenCalled();

    flushRaf(); // The outer rAF then schedules the inner one.
    flushRaf(); // The inner rAF then calls refit(), which calls fit().
    expect(fitSpy).toHaveBeenCalled();

    // The component now sends the first resizePty call (the bare fit never told the pty).
    vi.advanceTimersByTime(200);
    expect(w.resizePty).toHaveBeenCalledWith("paneInit", 80, 24);
  });

  // ── Window-resize backstop (A3) ─────────────────────────────────────────────
  it("re-fits on a window 'resize' even when the host ResizeObserver stays silent (backstop)", async () => {
    vi.useFakeTimers();
    const rafQueue: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((fn: FrameRequestCallback) => rafQueue.push(fn)) as any;
    const flushRaf = () => { rafQueue.splice(0).forEach((fn) => fn(0)); };

    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    render(Terminal, { props: { paneId: "paneWin", cwd: "/repo" } });
    // This settles the deferred mount fit, to isolate the resize-driven refit.
    flushRaf(); flushRaf();
    vi.advanceTimersByTime(200);
    expect(w.resizePty).toHaveBeenCalledWith("paneWin", 80, 24);
    fitSpy.mockClear();
    vi.mocked(w.resizePty).mockClear();

    // The box actually shrank. The default beforeEach ResizeObserver stub never
    // invokes its callback (observe() is a no-op). So only the window 'resize'
    // backstop can drive a refit here. This is exactly the WebKitGTK dropped-notification case.
    mockRows = 20;
    window.dispatchEvent(new Event("resize"));
    flushRaf(); // The coalesced rAF (shared rafId guard) then calls refit(), which calls fit().
    expect(fitSpy).toHaveBeenCalled();

    vi.advanceTimersByTime(200);
    expect(w.resizePty).toHaveBeenCalledWith("paneWin", 80, 20);
  });

  // ── Teardown cleans up the deferred frames + the resize listener (A4) ────────
  it("does not re-fit after unmount (pending frames guarded, resize listener removed)", async () => {
    vi.useFakeTimers();
    const rafQueue: FrameRequestCallback[] = [];
    globalThis.requestAnimationFrame = ((fn: FrameRequestCallback) => rafQueue.push(fn)) as any;
    const flushRaf = () => { rafQueue.splice(0).forEach((fn) => fn(0)); };

    const { default: Terminal } = await import("./Terminal.svelte");
    const w = await import("./wails");
    const { unmount } = render(Terminal, { props: { paneId: "paneDes", cwd: "/repo" } });

    // Tear down BEFORE the mount's double-rAF fires: the pending frames must not refit.
    unmount();
    flushRaf(); flushRaf();
    // And the window-resize backstop must be unregistered.
    window.dispatchEvent(new Event("resize"));
    flushRaf();
    vi.advanceTimersByTime(200);

    expect(fitSpy).not.toHaveBeenCalled();
    expect(w.resizePty).not.toHaveBeenCalled();
  });
});
