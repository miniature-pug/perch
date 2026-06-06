// frontend/src/lib/stores/settings.test.ts
import { vi, describe, it, expect, beforeEach } from "vitest";

vi.mock("../wails", () => ({
  getSettings: vi.fn(async () => ({
    theme: "gruvbox", density: "dense", font: "geist", dnd: false, glassDisabled: false, alwaysRules: [],
  })),
  saveSettings: vi.fn(async () => {}),
}));

beforeEach(() => { vi.clearAllMocks(); vi.resetModules(); });

describe("settings store", () => {
  it("load() populates state from getSettings", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    expect(vi.mocked(w.getSettings)).toHaveBeenCalledOnce();
    expect(settings.theme).toBe("gruvbox");
  });
  it("setTheme updates state and calls saveSettings", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setTheme("tokyo-night");
    expect(settings.theme).toBe("tokyo-night");
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ theme: "tokyo-night" }),
    );
  });
  it("setDnd updates and persists", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setDnd(true);
    expect(settings.dnd).toBe(true);
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ dnd: true }),
    );
  });
  it("load() defaults glass=true when glassDisabled is absent", async () => {
    const w = await import("../wails");
    vi.mocked(w.getSettings).mockResolvedValueOnce({
      theme: "gruvbox", density: "dense", font: "geist", dnd: false, alwaysRules: [],
    });
    const { settings } = await import("./settings.svelte");
    await settings.load();
    expect(settings.glass).toBe(true);
  });
  it("setGlass(false) persists glassDisabled=true", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setGlass(false);
    expect(settings.glass).toBe(false);
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ glassDisabled: true }),
    );
  });
  it("setGlass(true) persists glassDisabled=false", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setGlass(true);
    expect(settings.glass).toBe(true);
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ glassDisabled: false }),
    );
  });

  // Issue B: staleThresholdDays must round-trip through load → setTheme → saveSettings
  it("staleThresholdDays is preserved in SaveSettings payload after unrelated UI change", async () => {
    const w = await import("../wails");
    vi.mocked(w.getSettings).mockResolvedValueOnce({
      theme: "gruvbox", density: "dense", font: "geist", dnd: false,
      glassDisabled: false, alwaysRules: [], staleThresholdDays: 14,
    });
    const { settings } = await import("./settings.svelte");
    await settings.load();
    await settings.setTheme("tokyo-night");
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ staleThresholdDays: 14 }),
    );
  });
});
