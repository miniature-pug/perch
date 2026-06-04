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
  it("setSplitId stores the id and includes it in the persisted payload", async () => {
    const { layout } = await import("./layout.svelte");
    const w = await import("../wails");
    await layout.restore();
    layout.setSplitId("ws-42");
    expect(layout.splitId).toBe("ws-42");
    vi.advanceTimersByTime(400);
    expect(vi.mocked(w.saveLayout)).toHaveBeenCalledOnce();
    const payload = JSON.parse((vi.mocked(w.saveLayout).mock.calls[0] as [string])[0]);
    expect(payload.splitId).toBe("ws-42");
  });
  it("restore() hydrates splitId from persisted payload", async () => {
    const w = await import("../wails");
    vi.mocked(w.getLayout).mockResolvedValueOnce(
      JSON.stringify({ sidebarW: 240, shellH: 200, view: "agent", split: true, splitId: "ws-99", collapsed: {} })
    );
    const { layout } = await import("./layout.svelte");
    await layout.restore();
    expect(layout.splitId).toBe("ws-99");
  });

  // --- Behavior 4b: order field ---
  it("setOrder updates order and persists it", async () => {
    const { layout } = await import("./layout.svelte");
    const w = await import("../wails");
    await layout.restore();
    layout.setOrder(["ws-b", "ws-a", "ws-c"]);
    expect(layout.order).toEqual(["ws-b", "ws-a", "ws-c"]);
    vi.advanceTimersByTime(400);
    expect(vi.mocked(w.saveLayout)).toHaveBeenCalledOnce();
    const payload = JSON.parse((vi.mocked(w.saveLayout).mock.calls[0] as [string])[0]);
    expect(payload.order).toEqual(["ws-b", "ws-a", "ws-c"]);
  });

  it("restore() hydrates order from persisted payload", async () => {
    const w = await import("../wails");
    vi.mocked(w.getLayout).mockResolvedValueOnce(
      JSON.stringify({ sidebarW: 240, shellH: 200, view: "agent", split: false, collapsed: {}, order: ["ws-x", "ws-y"] })
    );
    const { layout } = await import("./layout.svelte");
    await layout.restore();
    expect(layout.order).toEqual(["ws-x", "ws-y"]);
  });

  it("restore() defaults order to [] when absent from payload", async () => {
    const w = await import("../wails");
    vi.mocked(w.getLayout).mockResolvedValueOnce(
      JSON.stringify({ sidebarW: 240, shellH: 200, view: "agent", split: false, collapsed: {} })
    );
    const { layout } = await import("./layout.svelte");
    await layout.restore();
    expect(layout.order).toEqual([]);
  });
});
