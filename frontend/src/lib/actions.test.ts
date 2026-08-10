// frontend/src/lib/actions.test.ts
// Unit tests for Svelte actions in actions.ts.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { countUp, trapFocus } from "./actions";

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

    // rAF has run. capturedCb is the first frame callback.
    expect(capturedCb).toBeDefined();

    // Cancel the in-flight animation.
    action.destroy();
    expect(cancelMock).toHaveBeenCalled();

    // Snapshot textContent right after destroy().
    const valueAfterDestroy = node.textContent;

    // Re-stub rAF to a spy, so the test can assert the spy is NOT called when the stale
    // callback fires. (The `destroyed` guard in the implementation makes the callback return
    // early. This prevents any further rAF scheduling.)
    const rafAfterDestroySpy = vi.fn().mockReturnValue(99);
    vi.stubGlobal("requestAnimationFrame", rafAfterDestroySpy);

    // This drives the stale callback directly. The browser would have suppressed the callback
    // via cancelAnimationFrame, but this test invokes the callback manually to prove the
    // `destroyed` flag silences it completely.
    if (capturedCb) capturedCb(50); // mid-animation timestamp

    // textContent must not have changed. The guard returned early.
    expect(node.textContent).toBe(valueAfterDestroy);
    // The code also schedules no new rAF.
    expect(rafAfterDestroySpy).not.toHaveBeenCalled();

    cleanup(node);
  });
});

// ---------------------------------------------------------------------------
// trapFocus
// ---------------------------------------------------------------------------

describe("trapFocus", () => {
  it("moves focus into the container on mount (first focusable)", () => {
    const bg = document.createElement("button");
    bg.textContent = "background";
    document.body.appendChild(bg);
    bg.focus();

    const container = document.createElement("div");
    const a = document.createElement("button");
    const b = document.createElement("button");
    container.append(a, b);
    document.body.appendChild(container);

    const action = trapFocus(container);
    expect(document.activeElement).toBe(a);

    action.destroy();
    container.remove();
    bg.remove();
  });

  it("focuses the element matching initialSelector when provided", () => {
    const container = document.createElement("div");
    const a = document.createElement("button");
    const b = document.createElement("input");
    b.setAttribute("data-init", "");
    container.append(a, b);
    document.body.appendChild(container);

    const action = trapFocus(container, "[data-init]");
    expect(document.activeElement).toBe(b);

    action.destroy();
    container.remove();
  });

  it("wraps focus from last to first on Tab", () => {
    const container = document.createElement("div");
    const a = document.createElement("button");
    const b = document.createElement("button");
    container.append(a, b);
    document.body.appendChild(container);

    const action = trapFocus(container);
    b.focus();

    const e = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    container.dispatchEvent(e);

    expect(e.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(a);

    action.destroy();
    container.remove();
  });

  it("wraps focus from first to last on Shift+Tab", () => {
    const container = document.createElement("div");
    const a = document.createElement("button");
    const b = document.createElement("button");
    container.append(a, b);
    document.body.appendChild(container);

    const action = trapFocus(container);
    a.focus();

    const e = new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });
    container.dispatchEvent(e);

    expect(e.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(b);

    action.destroy();
    container.remove();
  });

  it("restores focus to the trigger on destroy", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();

    const container = document.createElement("div");
    const a = document.createElement("button");
    container.append(a);
    document.body.appendChild(container);

    const action = trapFocus(container);
    expect(document.activeElement).toBe(a);

    action.destroy();
    expect(document.activeElement).toBe(trigger);

    container.remove();
    trigger.remove();
  });

  it("nested traps: inner trap owns Tab even when an outer focusable follows it in DOM order", () => {
    // Outer trap scrim: a focusable, then the inner trap container, placed BEFORE
    // a trailing outer focusable. This is the fragile DOM order the fix must handle.
    const outer = document.createElement("div");

    const outerFirst = document.createElement("button");
    outerFirst.textContent = "outer-first";

    const inner = document.createElement("div");
    const innerA = document.createElement("button");
    innerA.textContent = "inner-a";
    const innerB = document.createElement("button");
    innerB.textContent = "inner-b";
    inner.append(innerA, innerB);

    // Trailing outer focusable AFTER the inner container. Without the nesting
    // guard, the outer trap's `last` would be this element. Tab from innerB
    // would then leak here instead of wrapping to innerA.
    const outerLast = document.createElement("button");
    outerLast.textContent = "outer-last";

    outer.append(outerFirst, inner, outerLast);
    document.body.appendChild(outer);

    const outerAction = trapFocus(outer);
    const innerAction = trapFocus(inner);

    // Inner trap mounted last, so focus is inside it.
    expect(document.activeElement).toBe(innerA);

    // Tab from the inner tail must wrap to the inner head, NOT leak to outerLast,
    // even though the event bubbles to the outer trap's handler.
    innerB.focus();
    const eTab = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    innerB.dispatchEvent(eTab);
    expect(eTab.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(innerA);

    // Shift+Tab from the inner head wraps to the inner tail (still owned by inner).
    innerA.focus();
    const eShift = new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true });
    innerA.dispatchEvent(eShift);
    expect(eShift.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(innerB);

    // Once the inner trap is gone, the outer trap resumes normal ownership. Tab
    // from the outer tail wraps to the outer head across the whole scrim.
    innerAction.destroy();
    outerLast.focus();
    const eOuter = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    outerLast.dispatchEvent(eOuter);
    expect(eOuter.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(outerFirst);

    outerAction.destroy();
    outer.remove();
  });

  it("does not throw restoring focus when the trigger left the document", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();

    const container = document.createElement("div");
    container.append(document.createElement("button"));
    document.body.appendChild(container);

    const action = trapFocus(container);
    trigger.remove(); // trigger no longer in document

    expect(() => action.destroy()).not.toThrow();
    container.remove();
  });
});
