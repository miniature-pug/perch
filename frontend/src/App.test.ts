// frontend/src/App.test.ts
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

// Stub ShellDrawer (imports xterm which crashes jsdom).
vi.mock("./lib/ShellDrawer.svelte", async () => ({
  default: (await import("./lib/__stubs__/Empty.svelte")).default,
}));

// Stub heavy children — xterm/CodeMirror crash jsdom; wails calls in $effect would throw.
vi.mock("./lib/Terminal.svelte", async () => ({
  default: (await import("./lib/__stubs__/TerminalProbe.svelte")).default,
}));
vi.mock("./lib/Editor.svelte", async () => ({
  default: (await import("./lib/__stubs__/EditorProbe.svelte")).default,
}));
vi.mock("./lib/DiffView.svelte", async () => ({
  default: (await import("./lib/__stubs__/DiffProbe.svelte")).default,
}));
vi.mock("./lib/FileTree.svelte", async () => ({
  default: (await import("./lib/__stubs__/FileTreeProbe.svelte")).default,
}));

// Captured callbacks for the wails event helpers — reset in beforeEach.
const captured = {
  agent:     [] as Array<(ev: any) => void>,
  notify:    [] as Array<(n: any)  => void>,
  fsChanged: [] as Array<(p: any)  => void>,
};

vi.mock("./lib/wails", () => ({
  listWorkspaces:  vi.fn(async () => []),
  openWorkspace:   vi.fn(async () => {}),
  openShell:       vi.fn(async () => {}),
  getLayout:       vi.fn(async () => "{}"),
  saveLayout:      vi.fn(async () => {}),
  getSettings:     vi.fn(async () => ({})),
  saveSettings:    vi.fn(async () => {}),
  revealInFiles:   vi.fn(async () => {}),
  onAgentEvent:    vi.fn((cb) => { captured.agent.push(cb);     return () => {}; }),
  onNotify:        vi.fn((cb) => { captured.notify.push(cb);    return () => {}; }),
  onFsChanged:     vi.fn((cb) => { captured.fsChanged.push(cb); return () => {}; }),
}));

// NOTE: layout and mode stores are NOT mocked — we use the real $state runes stores.
// restore() calls getLayout() which is mocked to return "{}", so onMount is safe.
vi.mock("./lib/stores/settings.svelte", () => ({
  settings: {
    theme:   "gruvbox",
    density: "dense",
    load:       vi.fn(async () => {}),
    setTheme:   vi.fn(async () => {}),
    setDensity: vi.fn(async () => {}),
    setFont:    vi.fn(async () => {}),
    setDnd:     vi.fn(async () => {}),
  },
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

beforeEach(async () => {
  vi.clearAllMocks();
  // Reset captured callback arrays (vi.clearAllMocks does NOT empty them).
  captured.agent.length     = 0;
  captured.notify.length    = 0;
  captured.fsChanged.length = 0;
  // Reset the real layout singleton to default values before each test.
  const { layout } = await import("./lib/stores/layout.svelte");
  layout.setView("agent");
  layout.split = false as any;
  (layout as any).sidebarW  = 240;
  (layout as any).shellH    = 200;
  (layout as any).collapsed = {};
  // Reset the real mode singleton — must be "normal" for keymap guard to work.
  const { mode } = await import("./lib/stores/mode.svelte");
  mode.leaveCommand();
  (mode as any).current = "normal";
});

describe("App.svelte skeleton", () => {
  it("renders sidebar, stage, and shell-drawer zones", async () => {
    const { default: App } = await import("./App.svelte");
    render(App);
    expect(document.querySelector("[data-zone='sidebar']")).toBeInTheDocument();
    expect(document.querySelector("[data-zone='stage']")).toBeInTheDocument();
    expect(document.querySelector("[data-zone='shell-drawer']")).toBeInTheDocument();
  });
  it("pressing '1' in NORMAL calls layout.setView('agent')", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setView");
    const { default: App } = await import("./App.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "1" });
    expect(spy).toHaveBeenCalledWith("agent");
  });
  it("pressing '2' in NORMAL calls layout.setView('code')", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setView");
    const { default: App } = await import("./App.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "2" });
    expect(spy).toHaveBeenCalledWith("code");
  });
  it("pressing '\\' in NORMAL calls layout.toggleSplit", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "toggleSplit");
    const { default: App } = await import("./App.svelte");
    render(App);
    await fireEvent.keyDown(document.body, { key: "\\" });
    expect(spy).toHaveBeenCalled();
  });
  it("dragging sidebar divider calls layout.setSidebarW", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setSidebarW");
    const { default: App } = await import("./App.svelte");
    render(App);
    const divider = document.querySelector(".divider-v")!;
    await fireEvent.mouseDown(divider, { clientX: 240 });
    await fireEvent.mouseMove(window,  { clientX: 280 });
    await fireEvent.mouseUp(window);
    expect(spy).toHaveBeenCalled();
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

describe("App.svelte Stage content routing (4.25.2)", () => {
  it("view='agent' → TerminalProbe mounted with paneId and cwd from active workspace", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for workspaces to load (post-mount, so restore() has already run)
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    // Ensure we're in agent view (real store; restore() defaults view to "agent")
    layout.setView("agent");
    await tick();
    await fireEvent.click(alphaBtn);
    const terminal = await screen.findByTestId("terminal");
    expect(terminal).toBeInTheDocument();
    expect(terminal.dataset.paneId).toBe("p1");
    expect(terminal.dataset.cwd).toBe("/tmp/alpha");
  });

  it("view='code' → EditorProbe + FileTreeProbe mounted; FileTree open callback updates Editor path", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for workspaces to load (post-mount), then switch view reactively
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    layout.setView("code");
    await tick();
    const filetree = await screen.findByTestId("filetree");
    expect(filetree).toBeInTheDocument();
    expect(filetree.dataset.root).toBe("/tmp/alpha");
    const editor = screen.getByTestId("editor");
    expect(editor).toBeInTheDocument();
    expect(editor.dataset.worktree).toBe("/tmp/alpha");
    // Initially no path selected
    expect(editor.dataset.path).toBe("");
    // Clicking the FileTreeProbe's open button triggers onOpen → sets codePath
    const openBtn = screen.getByRole("button", { name: "open file" });
    await fireEvent.click(openBtn);
    await waitFor(() =>
      expect(screen.getByTestId("editor").dataset.path).toBe("/some/file.ts")
    );
  });

  it("view='diff' → DiffProbe mounted with active worktree", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for workspaces to load (post-mount), then switch view reactively
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    layout.setView("diff");
    await tick();
    const diff = await screen.findByTestId("diff");
    expect(diff).toBeInTheDocument();
    expect(diff.dataset.worktree).toBe("/tmp/alpha");
  });

  it("activeId null → no child probes, empty-state placeholder shown", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // Don't select any workspace — activeId stays null
    await screen.findByRole("button", { name: "Alpha" }); // workspaces loaded
    expect(document.querySelector(".empty-state")).toBeInTheDocument();
    expect(screen.queryByTestId("terminal")).not.toBeInTheDocument();
    expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
    expect(screen.queryByTestId("diff")).not.toBeInTheDocument();
  });

  it("reactive re-route: switching from 'agent' to 'code' swaps probes without re-render", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for workspaces to load (restore() has run by now)
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Start in agent view
    layout.setView("agent");
    await tick();
    expect(await screen.findByTestId("terminal")).toBeInTheDocument();
    expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
    expect(screen.queryByTestId("filetree")).not.toBeInTheDocument();
    // Reactively switch to code view
    layout.setView("code");
    await tick();
    await waitFor(() => {
      expect(screen.queryByTestId("terminal")).not.toBeInTheDocument();
      expect(screen.getByTestId("editor")).toBeInTheDocument();
      expect(screen.getByTestId("filetree")).toBeInTheDocument();
    });
  });

  it("split mode: view='agent' + split=true → two TerminalProbes rendered", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for workspaces to load (restore() has run by now)
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Set agent view and enable split
    layout.setView("agent");
    layout.toggleSplit(); // false → true
    await tick();
    // Both primary and secondary slots should have a TerminalProbe
    await waitFor(() => {
      const terminals = screen.getAllByTestId("terminal");
      expect(terminals).toHaveLength(2);
    });
  });
});

describe("App.svelte MenuBar + CommandPalette (4.25.3)", () => {
  it("pressing ':' in NORMAL opens the CommandPalette (dialog appears)", async () => {
    const { default: App } = await import("./App.svelte");
    render(App);
    // Palette must not be visible initially
    expect(screen.queryByRole("dialog", { name: "command palette" })).not.toBeInTheDocument();
    // Fire ':' keydown — onKeyDown calls mode.enterCommand() → mode.current = "command"
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
  });

  it("running 'view:code' via the palette sets layout.view to 'code' and closes the palette", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const { mode }   = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Open the palette
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
    // Click the "Code view" palette item
    const codeItem = screen.getByRole("option", { name: /code view/i });
    await fireEvent.click(codeItem);
    await tick();
    // Command ran: layout.view changed
    expect(layout.view).toBe("code");
    // Palette closed: mode back to normal
    expect(mode.current).toBe("normal");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "command palette" })).not.toBeInTheDocument()
    );
  });

  it("MenuBar 'view:split' command flips layout.split via the registry", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    expect(layout.split).toBe(false);
    // Open the View menu in the real MenuBar
    const viewMenuBtn = screen.getByRole("menuitem", { name: "View" });
    await fireEvent.click(viewMenuBtn);
    await tick();
    // Click the "Split" item in the dropdown
    const splitItem = screen.getByRole("menuitem", { name: "Split" });
    await fireEvent.click(splitItem);
    await tick();
    expect(layout.split).toBe(true);
  });
});

describe("App.svelte live event wiring (4.25.4)", () => {
  it("onAgentEvent: state flip updates Sidebar status label for that workspace", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for workspaces to load
    await screen.findByRole("button", { name: "Alpha" });

    // Alpha starts as "idle" → Sidebar shows "idle"
    expect(screen.getByRole("button", { name: "Alpha" })).toHaveTextContent("idle");

    // Fire an agent event that flips Alpha to "awaiting-approval" and carries an approval payload
    const cb = captured.agent.at(-1)!;
    cb({
      workspaceId: "ws-1",
      kind: "approval",
      state: "awaiting-approval",
      approval: { reqId: "req-1", tool: "bash", summary: "Run script" },
    });
    await tick();

    // Sidebar maps "awaiting-approval" → "needs you"
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Alpha" })).toHaveTextContent("needs you")
    );
  });

  it("onNotify: blocking tier calls addBlocking and appears in notifications store", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Alpha" });

    const { getItems } = await import("./lib/stores/notifications.svelte");
    const before = getItems().length;

    const cb = captured.notify.at(-1)!;
    cb({ tier: "blocking", title: "Needs approval", body: "Tool wants to run bash", workspaceId: "ws-1" });
    await tick();

    const items = getItems();
    expect(items.length).toBe(before + 1);
    expect(items[0].title).toBe("Needs approval");
    expect(items[0].tier).toBe("blocking");
    expect(items[0].workspaceId).toBe("ws-1");
  });

  it("onNotify: ambient tier routes to addAmbient in the store", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Alpha" });

    const { getItems, getDnd, setDnd } = await import("./lib/stores/notifications.svelte");
    // Ensure DND is off so ambient is not filtered
    setDnd(false);
    const before = getItems().length;

    const cb = captured.notify.at(-1)!;
    cb({ tier: "ambient", title: "Build complete", body: "Tests passed", workspaceId: "ws-2" });
    await tick();

    const items = getItems();
    expect(items.length).toBe(before + 1);
    expect(items[0].tier).toBe("ambient");
    expect(items[0].title).toBe("Build complete");
  });

  it("onFsChanged: bumps fsVersion → DiffProbe remounts (node identity changes)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha and switch to diff view
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    layout.setView("diff");
    await tick();

    const diffBefore = await screen.findByTestId("diff");
    expect(diffBefore).toBeInTheDocument();

    // Fire fs:changed for ws-1
    const cb = captured.fsChanged.at(-1)!;
    cb({ workspaceId: "ws-1", path: "/tmp/alpha/some-file.ts" });
    await tick();

    // {#key} remounts → a new DOM node is created
    await waitFor(() => {
      const diffAfter = screen.getByTestId("diff");
      expect(diffAfter).not.toBe(diffBefore);
    });
  });

  it("onDestroy: all off-fns are called on unmount (no event leaks)", async () => {
    const { listWorkspaces, onAgentEvent, onNotify, onFsChanged } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const offAgent     = vi.fn();
    const offNotify    = vi.fn();
    const offFsChanged = vi.fn();
    (onAgentEvent as ReturnType<typeof vi.fn>).mockReturnValueOnce(offAgent);
    (onNotify     as ReturnType<typeof vi.fn>).mockReturnValueOnce(offNotify);
    (onFsChanged  as ReturnType<typeof vi.fn>).mockReturnValueOnce(offFsChanged);

    const { default: App } = await import("./App.svelte");
    const { unmount } = render(App);
    // Give onMount (sync part) a chance to run
    await tick();

    unmount();
    await tick();

    expect(offAgent).toHaveBeenCalled();
    expect(offNotify).toHaveBeenCalled();
    expect(offFsChanged).toHaveBeenCalled();
  });
});
