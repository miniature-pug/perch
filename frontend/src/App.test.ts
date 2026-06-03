// frontend/src/App.test.ts
import { render, screen, fireEvent } from "@testing-library/svelte";
import { vi, describe, it, expect } from "vitest";

// Sidebar imports old wails exports — stub with an inert component.
vi.mock("./lib/Sidebar.svelte", async () => ({
  default: (await import("./lib/__stubs__/Empty.svelte")).default,
}));

vi.mock("./lib/stores/layout.svelte", () => ({
  layout: {
    sidebarW: 240, shellH: 200, view: "agent", split: false, collapsed: {},
    restore:     vi.fn(async () => {}),
    setView:     vi.fn(),
    toggleSplit: vi.fn(),
    setSidebarW: vi.fn(),
    setShellH:   vi.fn(),
  },
}));
vi.mock("./lib/stores/mode.svelte", () => ({
  mode: { current: "normal", enterTerminal: vi.fn(), enterCommand: vi.fn(), leaveCommand: vi.fn() },
}));
vi.mock("./lib/stores/settings.svelte", () => ({
  settings: { theme: "gruvbox", density: "dense", load: vi.fn(async () => {}) },
}));

describe("App.svelte skeleton", () => {
  it("renders sidebar, stage, and shell-drawer zones", async () => {
    const { default: App } = await import("./App.svelte");
    render(App);
    expect(document.querySelector("[data-zone='sidebar']")).toBeInTheDocument();
    expect(document.querySelector("[data-zone='stage']")).toBeInTheDocument();
    expect(document.querySelector("[data-zone='shell-drawer']")).toBeInTheDocument();
  });
  it("pressing '1' in NORMAL calls layout.setView('agent')", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "1" });
    expect(layout.setView).toHaveBeenCalledWith("agent");
  });
  it("pressing '2' in NORMAL calls layout.setView('code')", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "2" });
    expect(layout.setView).toHaveBeenCalledWith("code");
  });
  it("pressing '\\' in NORMAL calls layout.toggleSplit", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "\\" });
    expect(layout.toggleSplit).toHaveBeenCalled();
  });
  it("dragging sidebar divider calls layout.setSidebarW", async () => {
    const { default: App } = await import("./App.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    render(App);
    const divider = document.querySelector(".divider-v")!;
    await fireEvent.mouseDown(divider, { clientX: 240 });
    await fireEvent.mouseMove(window,  { clientX: 280 });
    await fireEvent.mouseUp(window);
    expect(layout.setSidebarW).toHaveBeenCalled();
  });
});
