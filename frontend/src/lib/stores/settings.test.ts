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

  // A UI-pref save must preserve an Always rule the backend appended AFTER load.
  // Approve(...,"always") writes to the settings blob with no event, so the store's
  // in-memory alwaysRules is stale; persistPref re-reads it before saving.
  it("setTheme preserves an alwaysRule added to the backend after load", async () => {
    const w = await import("../wails");
    // load() sees no rules...
    vi.mocked(w.getSettings).mockResolvedValueOnce({
      theme: "gruvbox", density: "dense", font: "geist", dnd: false, glassDisabled: false, alwaysRules: [],
    });
    const { settings } = await import("./settings.svelte");
    await settings.load();
    expect(settings.alwaysRules).toEqual([]);
    // ...then the backend appends a rule (e.g. user clicked "always" in a card).
    const rule = { agent: "claude", tool: "Bash", pattern: "ls", hash: "abc" };
    vi.mocked(w.getSettings).mockResolvedValueOnce({
      theme: "gruvbox", density: "dense", font: "geist", dnd: false, glassDisabled: false, alwaysRules: [rule],
    });
    await settings.setTheme("tokyo-night");
    // The save payload must carry the backend-added rule, not the stale empty list.
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ theme: "tokyo-night", alwaysRules: [rule] }),
    );
    expect(settings.alwaysRules).toEqual([rule]);
  });

  // setAlwaysRules is the authoritative writer — it must NOT re-read (that would
  // race its own write) and must persist exactly what it was given.
  it("setAlwaysRules writes the given rules without re-reading the backend", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    vi.mocked(w.getSettings).mockClear();
    const rules = [{ agent: "claude", tool: "Edit", pattern: "x", hash: "h" }];
    await settings.setAlwaysRules(rules);
    expect(vi.mocked(w.getSettings)).not.toHaveBeenCalled();
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ alwaysRules: rules }),
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

  // WIN #1 — setStaleThresholdDays mirrors the other setters exactly (update
  // state, then persist through the shared persistPref path).
  it("setStaleThresholdDays updates state and calls saveSettings with the new value", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setStaleThresholdDays(45);
    expect(settings.staleThresholdDays).toBe(45);
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ staleThresholdDays: 45 }),
    );
  });

  it("setStaleThresholdDays(0) is accepted and persisted as 0 (backend treats <=0 as default)", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setStaleThresholdDays(0);
    expect(settings.staleThresholdDays).toBe(0);
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ staleThresholdDays: 0 }),
    );
  });

  it("setStaleThresholdDays(undefined) is accepted and persisted as undefined (blank input)", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setStaleThresholdDays(undefined);
    expect(settings.staleThresholdDays).toBeUndefined();
    expect(vi.mocked(w.saveSettings)).toHaveBeenCalledWith(
      expect.objectContaining({ staleThresholdDays: undefined }),
    );
  });

  it("a valid positive integer round-trips through load after setStaleThresholdDays + a fresh getSettings", async () => {
    const { settings } = await import("./settings.svelte");
    const w = await import("../wails");
    await settings.load();
    await settings.setStaleThresholdDays(21);
    vi.mocked(w.getSettings).mockResolvedValueOnce({
      theme: "gruvbox", density: "dense", font: "geist", dnd: false,
      glassDisabled: false, alwaysRules: [], staleThresholdDays: 21,
    });
    await settings.load();
    expect(settings.staleThresholdDays).toBe(21);
  });
});
