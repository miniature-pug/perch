// frontend/src/lib/stores/layout.test.ts
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

vi.mock("../wails", () => ({
  getLayout: vi.fn(async () =>
    JSON.stringify({ sidebarW: 240, shellH: 200, view: "agent", split: false, collapsed: {} }),
  ),
  saveLayout: vi.fn(async () => {}),
}));

beforeEach(() => { vi.clearAllMocks(); vi.resetModules(); vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); });

describe("layout store", () => {
  it("restore() loads from getLayout", async () => {
    const { layout } = await import("./layout.svelte");
    const w = await import("../wails");
    await layout.restore();
    expect(vi.mocked(w.getLayout)).toHaveBeenCalledOnce();
    expect(layout.sidebarW).toBe(240);
    expect(layout.view).toBe("agent");
  });
  it("setSidebarW debounces saveLayout", async () => {
    const { layout } = await import("./layout.svelte");
    const w = await import("../wails");
    await layout.restore();
    layout.setSidebarW(300);
    layout.setSidebarW(320);
    expect(vi.mocked(w.saveLayout)).not.toHaveBeenCalled();
    vi.advanceTimersByTime(400);
    expect(vi.mocked(w.saveLayout)).toHaveBeenCalledOnce();
    expect(JSON.parse((vi.mocked(w.saveLayout).mock.calls[0] as [string])[0]).sidebarW).toBe(320);
  });
  it("setView updates view", async () => {
    const { layout } = await import("./layout.svelte");
    await layout.restore();
    layout.setView("diff");
    expect(layout.view).toBe("diff");
  });
  it("toggleSplit flips split", async () => {
    const { layout } = await import("./layout.svelte");
    await layout.restore();
    layout.toggleSplit();
    expect(layout.split).toBe(true);
  });
});
