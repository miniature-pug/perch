// frontend/src/lib/actions.test.ts
// Unit tests for Svelte actions in actions.ts.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { countUp } from "./actions";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeNode(): HTMLElement {
  const el = document.createElement("span");
  document.body.appendChild(el);
  return el;
}

function cleanup(el: HTMLElement): void {
  el.remove();
}

// ---------------------------------------------------------------------------
// countUp
// ---------------------------------------------------------------------------

describe("countUp", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sets textContent to initial value synchronously on mount (no animation)", () => {
    const node = makeNode();
    const action = countUp(node, 5);
    expect(node.textContent).toBe("5");
    action.destroy();
    cleanup(node);
  });

  it("sets textContent immediately when prefers-reduced-motion is true", () => {
    const node = makeNode();
    const action = countUp(node, 5);

    // Mock matchMedia to indicate reduced motion preference.
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query === "(prefers-reduced-motion: reduce)",
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));

    action.update(42);
    expect(node.textContent).toBe("42");

    action.destroy();
    cleanup(node);
  });

  it("reaches the exact final value after animation completes via rAF shim", () => {
    const node = makeNode();
    const action = countUp(node, 10);

    // Stub matchMedia to NOT trigger reduced-motion path.
    vi.stubGlobal("matchMedia", (_query: string) => ({
      matches: false,
      media: _query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));

    // Capture rAF callbacks and drive them manually with advancing timestamps.
    type FrameCallback = (timestamp: number) => void;
    const pendingFrames: Map<number, FrameCallback> = new Map();
    let rafCounter = 1;

    vi.stubGlobal("requestAnimationFrame", (cb: FrameCallback): number => {
      const id = rafCounter++;
      pendingFrames.set(id, cb);
      return id;
    });

    vi.stubGlobal("cancelAnimationFrame", (id: number): void => {
      pendingFrames.delete(id);
    });

    // Drive the animation: advance timestamps past the full duration (380ms default).
    function driveFrames(untilDone: boolean): void {
      const STEP = 20; // ms per synthetic frame
      let t = 0;
      while (pendingFrames.size > 0) {
        t += STEP;
        const toRun = [...pendingFrames.entries()];
        pendingFrames.clear();
        for (const [, cb] of toRun) cb(t);
        if (!untilDone) break;
        if (t > 1000) break; // safety guard
      }
    }

    action.update(99);

    // Drive all frames to completion.
    driveFrames(true);

    expect(node.textContent).toBe("99");

    action.destroy();
    cleanup(node);
  });

  it("destroy() cancels in-flight animation (no further textContent writes)", () => {
    const node = makeNode();
    const action = countUp(node, 0);

    vi.stubGlobal("matchMedia", (_query: string) => ({
      matches: false,
      media: _query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));

    const cancelMock = vi.fn();

    type FrameCallback = (timestamp: number) => void;
    let capturedCb: FrameCallback | undefined;
    let rafId = 1;

    vi.stubGlobal("requestAnimationFrame", (cb: FrameCallback): number => {
      capturedCb = cb;
      return rafId++;
    });
    vi.stubGlobal("cancelAnimationFrame", cancelMock);

    action.update(100);

    // rAF has been called — capturedCb is the first frame callback.
    expect(capturedCb).toBeDefined();

    // Cancel the in-flight animation.
    action.destroy();
    expect(cancelMock).toHaveBeenCalled();

    // Snapshot textContent right after destroy().
    const valueAfterDestroy = node.textContent;

    // Re-stub rAF to a spy so we can assert it is NOT called when the stale
    // callback fires (the `destroyed` guard in the implementation makes it return
    // early, preventing any further rAF scheduling).
    const rafAfterDestroySpy = vi.fn().mockReturnValue(99);
    vi.stubGlobal("requestAnimationFrame", rafAfterDestroySpy);

    // Drive the stale callback directly — the browser would have suppressed it
    // via cancelAnimationFrame, but here we invoke it manually to prove the
    // `destroyed` flag silences it completely.
    if (capturedCb) capturedCb(50); // mid-animation timestamp

    // textContent must not have changed — the guard returned early.
    expect(node.textContent).toBe(valueAfterDestroy);
    // And no new rAF was scheduled.
    expect(rafAfterDestroySpy).not.toHaveBeenCalled();

    cleanup(node);
  });
});
