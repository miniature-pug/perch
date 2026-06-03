// frontend/src/App.test.ts
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach } from "vitest";

// Stub ShellDrawer (imports xterm which crashes jsdom).
vi.mock("./lib/ShellDrawer.svelte", async () => ({
  default: (await import("./lib/__stubs__/Empty.svelte")).default,
}));

vi.mock("./lib/wails", () => ({
  listWorkspaces: vi.fn(async () => []),
  openWorkspace:  vi.fn(async () => {}),
  openShell:      vi.fn(async () => {}),
  getLayout:      vi.fn(async () => "{}"),
  saveLayout:     vi.fn(async () => {}),
  getSettings:    vi.fn(async () => ({})),
  saveSettings:   vi.fn(async () => {}),
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

const fakeWorkspaces = [
  {
    id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
    worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
    caps: { approvals: false, attention: false, tokens: false },
  },
  {
    id: "ws-2", title: "Beta", branch: "feat/beta", state: "running" as const,
    worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
    caps: { approvals: false, attention: false, tokens: false },
  },
];

beforeEach(() => vi.clearAllMocks());

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

describe("App.svelte workspace wiring (4.25.1)", () => {
  it("renders workspaces returned by listWorkspaces in the Sidebar", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // Sidebar renders each ws as a button with aria-label={ws.title}
    expect(await screen.findByRole("button", { name: "Alpha" })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Beta" })).toBeInTheDocument();
  });

  it("selecting a workspace calls openWorkspace(id) and marks it active", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    expect(openWorkspace).toHaveBeenCalledWith("ws-1");
    await waitFor(() =>
      expect(alphaBtn).toHaveAttribute("aria-current", "page")
    );
    expect(screen.getByRole("button", { name: "Beta" })).not.toHaveAttribute("aria-current");
  });
});
