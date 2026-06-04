// frontend/src/App.test.ts
import { render, screen, fireEvent, waitFor, within } from "@testing-library/svelte";
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

// Editor stub: renders the standard probe div AND exposes a "send to agent" button
// so App.test.ts can verify that onSendToAgent prop is wired and writeToPty fires.
// Uses svelte/internal/client APIs (from_html) so DOM nodes are properly tracked for
// unmount — from_html's factory calls assign_nodes which registers start/end nodes
// with the active Svelte effect, enabling correct {#if} branch teardown.
let _editorSendToAgent: ((text: string) => void) | undefined;
vi.mock("./lib/Editor.svelte", async () => {
  // eslint-disable-next-line @typescript-eslint/ban-ts-comment
  // @ts-ignore — svelte/internal/client is a private module with no type declarations.
  const $ = await import("svelte/internal/client");
  // Root template: a div containing a trigger button.
  // from_html returns a factory; each call clones the template and calls assign_nodes.
  const editorRoot = $.from_html(
    `<div data-testid="editor"><button>send to agent</button></div>`,
  );
  return {
    default: function MockEditor($$anchor: any, $$props: any) {
      $.push($$props, true);
      _editorSendToAgent = $$props.onSendToAgent;
      const div = editorRoot() as HTMLElement;
      const btn = div.querySelector("button")!;
      btn.onclick = () => ($$props.onSendToAgent as any)?.("hello from editor");
      $.template_effect(() => {
        $.set_attribute(div, "data-path", $$props.path ?? "");
        $.set_attribute(div, "data-worktree", $$props.worktree ?? "");
      });
      $.append($$anchor, div);
      $.pop();
    },
  };
});
vi.mock("./lib/DiffView.svelte", async () => ({
  default: (await import("./lib/__stubs__/DiffProbe.svelte")).default,
}));
vi.mock("./lib/FileTree.svelte", async () => ({
  default: (await import("./lib/__stubs__/FileTreeProbe.svelte")).default,
}));
vi.mock("./lib/Preview.svelte", async () => ({
  default: (await import("./lib/__stubs__/PreviewProbe.svelte")).default,
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
  closeWorkspace:  vi.fn(async () => {}),
  openShell:       vi.fn(async () => {}),
  getLayout:       vi.fn(async () => "{}"),
  saveLayout:      vi.fn(async () => {}),
  getSettings:     vi.fn(async () => ({})),
  saveSettings:    vi.fn(async () => {}),
  revealInFiles:   vi.fn(async () => {}),
  readFile:        vi.fn(async () => "# mock content"),
  approve:         vi.fn(async () => {}),
  createWorkspace: vi.fn(async (_agent: string, _repo: string, _branch: string, _model: string) => ({
    id: "ws-new", title: "New", branch: "main", state: "idle",
    worktreePath: "/tmp/new", agent: "claude", paneId: "p-new", lastActive: "",
    caps: { approvals: false, attention: false, tokens: false },
  })),
  removeWorkspace: vi.fn(async () => {}),
  writeToPty:      vi.fn(async () => {}),
  branches:        vi.fn(async (_repo: string) => ["main", "feat/x"]),
  onAgentEvent:    vi.fn((cb) => { captured.agent.push(cb);     return () => {}; }),
  onNotify:        vi.fn((cb) => { captured.notify.push(cb);    return () => {}; }),
  onFsChanged:     vi.fn((cb) => { captured.fsChanged.push(cb); return () => {}; }),
  setWindowFocus:  vi.fn(async () => {}),
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

  it("code view: .md path → PreviewProbe mounts (NOT Editor); .go path → Editor mounts (NOT Preview)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    layout.setView("code");
    await tick();

    // Open a .md file via the FileTreeProbe "open markdown" button
    const openMdBtn = screen.getByRole("button", { name: "open markdown" });
    await fireEvent.click(openMdBtn);
    await waitFor(() => {
      expect(screen.getByTestId("preview")).toBeInTheDocument();
      expect(screen.getByTestId("preview").dataset.path).toBe("/some/file.md");
      expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
    });

    // Open a .ts file via the FileTreeProbe "open file" button → Editor mounts, Preview gone
    const openTsBtn = screen.getByRole("button", { name: "open file" });
    await fireEvent.click(openTsBtn);
    await waitFor(() => {
      expect(screen.getByTestId("editor")).toBeInTheDocument();
      expect(screen.queryByTestId("preview")).not.toBeInTheDocument();
    });
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

  it("readFile rejection for a .md path: App does not crash and PreviewProbe still mounts", async () => {
    const { listWorkspaces, readFile } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    vi.mocked(readFile).mockRejectedValueOnce(new Error("gone"));
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    layout.setView("code");
    await tick();

    // Open a .md file — readFile will reject
    const openMdBtn = screen.getByRole("button", { name: "open markdown" });
    await fireEvent.click(openMdBtn);

    // App must not crash; Preview probe must still mount with the .md path
    await waitFor(() => {
      expect(screen.getByTestId("preview")).toBeInTheDocument();
      expect(screen.getByTestId("preview").dataset.path).toBe("/some/file.md");
    });
    // Editor must not be present (Preview routing is correct)
    expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
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

describe("App.svelte approval card + notification hub (4.25.5)", () => {
  // Workspace with caps.approvals=true so ApprovalCard actually renders.
  const approvalWorkspaces = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: true, attention: false, tokens: false },
    },
  ];

  it("ApprovalCard renders in docked chrome when active workspace has a pending approval", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(approvalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select the workspace to make it active
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();

    // Inject approval event for ws-1
    const cb = captured.agent.at(-1)!;
    cb({
      workspaceId: "ws-1",
      kind: "approval",
      state: "awaiting-approval",
      approval: { reqId: "req-42", tool: "bash", summary: "Run the test suite" },
    });
    await tick();

    // Card should be visible — check for summary text and Allow button
    await waitFor(() => {
      expect(screen.getByText("Run the test suite")).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Allow" })).toBeInTheDocument();
    });
  });

  it("clicking Allow calls approve(reqId, 'allow') and dequeues the card", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(approvalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select the workspace to make it active
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();

    // Inject approval event
    const cb = captured.agent.at(-1)!;
    cb({
      workspaceId: "ws-1",
      kind: "approval",
      state: "awaiting-approval",
      approval: { reqId: "req-99", tool: "bash", summary: "Deploy to prod" },
    });
    await tick();

    // Wait for card to appear
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Allow" })).toBeInTheDocument()
    );

    // Click Allow
    await fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await tick();

    // approve() called with the right args
    expect(approve).toHaveBeenCalledWith("req-99", "allow");

    // Card disappears after dequeue
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument()
    );
  });

  it("ApprovalCard is docked OUTSIDE the stage grid — not a descendant of [data-zone='stage']", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(approvalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();

    const cb = captured.agent.at(-1)!;
    cb({
      workspaceId: "ws-1",
      kind: "approval",
      state: "awaiting-approval",
      approval: { reqId: "req-dock", tool: "bash", summary: "Unique docking summary text" },
    });
    await tick();

    // Summary text must be visible in the document
    await waitFor(() =>
      expect(screen.getByText("Unique docking summary text")).toBeInTheDocument()
    );

    // But must NOT be inside the stage zone
    const stageEl = document.querySelector<HTMLElement>("[data-zone='stage']")!;
    expect(stageEl).toBeInTheDocument();
    expect(within(stageEl).queryByText("Unique docking summary text")).toBeNull();
  });

  it("approve RPC failure: card stays visible and adds a blocking notification", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(approvalWorkspaces);
    (vi.mocked(approve)).mockRejectedValueOnce(new Error("boom"));
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select workspace
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();

    // Inject approval
    const cb = captured.agent.at(-1)!;
    cb({
      workspaceId: "ws-1",
      kind: "approval",
      state: "awaiting-approval",
      approval: { reqId: "req-fail", tool: "bash", summary: "Failing approval" },
    });
    await tick();

    // Wait for card to appear
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Allow" })).toBeInTheDocument()
    );

    // Click Allow — approve will reject
    await fireEvent.click(screen.getByRole("button", { name: "Allow" }));

    // (a) ApprovalCard must still be present — not dequeued
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Allow" })).toBeInTheDocument()
    );

    // (b) A blocking notification with title "Approval failed" must be in the store
    const { getItems } = await import("./lib/stores/notifications.svelte");
    await waitFor(() =>
      expect(getItems().some((n) => n.title === "Approval failed")).toBe(true)
    );
  });

  it("NotificationHub renders and shows notifications from the store (hub opened via bell)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(approvalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: "Alpha" });

    // Inject a notification via the notify callback
    const cb = captured.notify.at(-1)!;
    cb({ tier: "blocking", title: "Hub test notification", body: "Hub body", workspaceId: "ws-1" });
    await tick();

    // NotificationHub is now behind {#if notifOpen} — open it first via the bell
    const bellBtn = screen.getByRole("button", { name: "notifications" });
    await fireEvent.click(bellBtn);
    await tick();

    // Hub must now be visible
    expect(screen.getByRole("region", { name: "notification hub" })).toBeInTheDocument();

    // The notification title should appear in the hub
    await waitFor(() =>
      expect(screen.getByText("Hub test notification")).toBeInTheDocument()
    );
  });
});

// ---------------------------------------------------------------------------
// 4.25.6a: Dialogs + DragDrop + TokenMeter + usage storage
// ---------------------------------------------------------------------------

describe("App.svelte TokenMeter + usage storage (4.25.6a)", () => {
  const tokenWorkspaces = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: false, attention: false, tokens: true }, // caps.tokens=true → meter visible
    },
  ];

  it("usage agent event for active workspace makes TokenMeter show those tokens", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(tokenWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select ws-1 to make it active
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();

    // Fire a usage event
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "usage", tokens: 12345, cost: 0.07 });
    await tick();

    // TokenMeter (caps.tokens=true) should render and show the token count
    await waitFor(() => {
      const meter = screen.getByRole("status", { name: "token usage" });
      expect(meter).toBeInTheDocument();
      expect(meter.textContent).toContain("12.3k");
    });
  });
});

describe("App.svelte NewSessionDialog (4.25.6a)", () => {
  it("Sidebar onNew / openNewSession opens the dialog; submitting calls createWorkspace and refreshes", async () => {
    const { listWorkspaces, createWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        {
          id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
          worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
          caps: { approvals: false, attention: false, tokens: false },
        },
      ])
      .mockResolvedValue([]); // subsequent listWorkspaces after create

    const { default: App } = await import("./App.svelte");
    render(App);

    // Workspaces loaded
    await screen.findByRole("button", { name: "Alpha" });

    // Dialog must not be visible yet
    expect(screen.queryByRole("dialog", { name: "new session" })).not.toBeInTheDocument();

    // Click the Sidebar "New session" CTA — triggers onNew → openNewSession → newSessionOpen=true
    const newBtn = screen.getByRole("button", { name: "New session" });
    await fireEvent.click(newBtn);
    await tick();

    // Dialog should now be visible
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );

    // Wait for branch options to load from the mock loadBranches
    await waitFor(() => {
      const branchSelect = screen.getByLabelText(/branch/i) as HTMLSelectElement;
      expect(branchSelect.options.length).toBeGreaterThan(0);
    });

    // Select specific values so we can assert exact args
    await fireEvent.change(screen.getByLabelText(/repo/i),   { target: { value: "/tmp/alpha" } });
    await waitFor(() => {
      const branchSelect = screen.getByLabelText(/branch/i) as HTMLSelectElement;
      expect(branchSelect.options.length).toBeGreaterThan(0);
    });
    await fireEvent.change(screen.getByLabelText(/branch/i), { target: { value: "feat/x" } });
    await fireEvent.change(screen.getByLabelText(/model/i),  { target: { value: "claude-sonnet-4-5" } });

    // Hit the "Create" button inside the dialog
    const createBtn = screen.getByRole("button", { name: "Create" });
    await fireEvent.click(createBtn);
    await tick();

    // createWorkspace must have been called with the selected agent/repo/branch/model
    expect(createWorkspace).toHaveBeenCalledWith("claude", "/tmp/alpha", "feat/x", "claude-sonnet-4-5");

    // Dialog must close
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "new session" })).not.toBeInTheDocument()
    );
  });

  it("session:new command also opens NewSessionDialog", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // Open palette and run session:new
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
    const newItem = screen.getByRole("option", { name: /new session/i });
    await fireEvent.click(newItem);
    await tick();

    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );
  });
});

describe("App.svelte ConfirmDialog (workspace remove) (4.25.6a)", () => {
  it("session:remove command shows ConfirmDialog; confirming calls removeWorkspace(id)", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false, tokens: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select ws-1 to make it active (required for session:remove to find active)
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();

    // No confirm dialog yet
    expect(screen.queryByRole("dialog", { name: "confirm" })).not.toBeInTheDocument();

    // Open palette and run session:remove
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
    const removeItem = screen.getByRole("option", { name: /remove session/i });
    await fireEvent.click(removeItem);
    await tick();

    // ConfirmDialog must appear
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "confirm" })).toBeInTheDocument()
    );

    // Click the "Remove" confirm button
    const confirmBtn = screen.getByRole("button", { name: "Remove" });
    await fireEvent.click(confirmBtn);
    await tick();

    // removeWorkspace called with the active workspace id
    expect(removeWorkspace).toHaveBeenCalledWith("ws-1");

    // Dialog closes
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "confirm" })).not.toBeInTheDocument()
    );
  });
});

describe("App.svelte DragDrop (4.25.6a)", () => {
  it("dropping a file onto the agent terminal writes @path bytes via writeToPty", async () => {
    const { listWorkspaces, writeToPty } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false, tokens: false },
      },
    ]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select ws-1 and go to agent view
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    layout.setView("agent");
    await tick();

    // Get the DragDrop drop-zone
    const dropZone = await screen.findByRole("region", { name: "drop zone" });
    expect(dropZone).toBeInTheDocument();

    // Create a File with a .path property (Wails/Electron-style)
    const file = Object.assign(new File(["content"], "foo.ts"), { path: "/tmp/alpha/foo.ts" });

    // Fire the drop event
    await fireEvent.drop(dropZone, {
      dataTransfer: { files: [file] },
    });
    await tick();

    // writeToPty should have been called with the paneId and bytes encoding "@/tmp/alpha/foo.ts "
    await waitFor(() => {
      expect(writeToPty).toHaveBeenCalled();
      const [calledPaneId, calledBytes] = (writeToPty as ReturnType<typeof vi.fn>).mock.calls[0];
      expect(calledPaneId).toBe("p1");
      const decoded = new TextDecoder().decode(new Uint8Array(calledBytes));
      expect(decoded).toBe("@/tmp/alpha/foo.ts ");
    });
  });
});

// ---------------------------------------------------------------------------
// 4.25.6b: Full NORMAL keymap + mode state machine
// ---------------------------------------------------------------------------

describe("App.svelte keymap: j/k navigation (4.25.6b)", () => {
  it("j moves activeId DOWN through the workspace list (clamp at end); k moves UP (clamp at start)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Alpha" });
    await tick();

    // Initially no active — j selects first
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Alpha" })).toHaveAttribute("aria-current", "page")
    );

    // j again → Beta
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Beta" })).toHaveAttribute("aria-current", "page")
    );

    // j again at end → stays Beta (clamp)
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Beta" })).toHaveAttribute("aria-current", "page")
    );

    // k → Alpha
    await fireEvent.keyDown(document.body, { key: "k" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Alpha" })).toHaveAttribute("aria-current", "page")
    );

    // k at start → stays Alpha (clamp)
    await fireEvent.keyDown(document.body, { key: "k" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Alpha" })).toHaveAttribute("aria-current", "page")
    );

    // j/k must NEVER call openWorkspace
    expect(openWorkspace).not.toHaveBeenCalled();
  });
});

describe("App.svelte keymap: Enter opens focused session (4.25.6b)", () => {
  it("Enter with activeId calls openWorkspace(activeId)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Alpha" });
    await tick();

    // Select Alpha via j
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    // Reset mock call count (might have been called by click in other tests — not here)
    vi.mocked(openWorkspace).mockClear();

    // Enter → open
    await fireEvent.keyDown(document.body, { key: "Enter" });
    await tick();
    expect(openWorkspace).toHaveBeenCalledWith("ws-1");
  });
});

describe("App.svelte keymap: g-prefix sequences (4.25.6b)", () => {
  it("gd sets view to 'diff'", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("agent");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "d" });
    await tick();
    expect(layout.view).toBe("diff");
  });

  it("ge sets view to 'code'", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("agent");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "e" });
    await tick();
    expect(layout.view).toBe("code");
  });

  it("lone g followed by unrelated key does NOT change the view", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("agent");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "x" });
    await tick();
    expect(layout.view).toBe("agent");
  });
});

describe("App.svelte keymap: Ctrl-` toggles shell (4.25.6b)", () => {
  it("Ctrl-` flips layout.collapsed['shell']", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    expect(layout.collapsed["shell"]).toBeFalsy();

    await fireEvent.keyDown(document.body, { key: "`", ctrlKey: true });
    await tick();
    expect(layout.collapsed["shell"]).toBe(true);

    await fireEvent.keyDown(document.body, { key: "`", ctrlKey: true });
    await tick();
    expect(layout.collapsed["shell"]).toBe(false);
  });
});

describe("App.svelte keymap: filter UI (4.25.6b)", () => {
  it("'/' shows filter input; typing filters Sidebar items; Esc hides it and restores full list", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Alpha" });
    await tick();

    // Filter input not visible initially
    expect(screen.queryByRole("textbox", { name: "filter sessions" })).not.toBeInTheDocument();

    // Press '/' to open filter
    await fireEvent.keyDown(document.body, { key: "/" });
    await tick();
    const filterInput = await screen.findByRole("textbox", { name: "filter sessions" });
    expect(filterInput).toBeInTheDocument();

    // Filter input must receive focus immediately on mount so keystrokes reach it, not the window keymap.
    expect(document.activeElement).toBe(filterInput);

    // Both workspaces visible initially
    expect(screen.getByRole("button", { name: "Alpha" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Beta" })).toBeInTheDocument();

    // Type "alph" — only Alpha should remain
    await fireEvent.input(filterInput, { target: { value: "alph" } });
    await tick();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Alpha" })).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Beta" })).not.toBeInTheDocument();
    });

    // Esc hides filter and restores full list
    await fireEvent.keyDown(filterInput, { key: "Escape" });
    await tick();
    await waitFor(() => {
      expect(screen.queryByRole("textbox", { name: "filter sessions" })).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Alpha" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Beta" })).toBeInTheDocument();
    });
  });
});

describe("App.svelte keymap: mode transitions (4.25.6b)", () => {
  it("'i' in NORMAL → mode becomes 'terminal'", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    await fireEvent.keyDown(document.body, { key: "i" });
    await tick();
    expect(mode.current).toBe("terminal");
  });

  it("':' in NORMAL → mode becomes 'command'", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    expect(mode.current).toBe("command");
  });

  it("Ctrl-K in NORMAL → mode becomes 'command' (spec §7.7 command palette shortcut)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    await fireEvent.keyDown(document.body, { key: "k", ctrlKey: true });
    await tick();
    expect(mode.current).toBe("command");
  });

  it("Ctrl-K does NOT open command palette when focus is inside an input", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // Open the filter input (/) so there is a focused input in the DOM.
    await fireEvent.keyDown(document.body, { key: "/" });
    await tick();
    const filterInput = await screen.findByRole("textbox", { name: "filter sessions" });
    // Fire Ctrl-K on the input element itself (target = INPUT).
    await fireEvent.keyDown(filterInput, { key: "k", ctrlKey: true });
    await tick();
    // Mode must remain normal — Ctrl-K must not fire inside an input.
    expect(mode.current).toBe("normal");
  });
});

describe("App.svelte keymap: TERMINAL leave sequence (4.25.6b)", () => {
  it("Ctrl-\\ then Ctrl-n returns mode to 'normal'", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    mode.enterTerminal();
    expect(mode.current).toBe("terminal");

    await fireEvent.keyDown(document.body, { key: "\\", ctrlKey: true });
    await tick();
    // Still terminal — pendingLeave set but not left yet
    expect(mode.current).toBe("terminal");

    await fireEvent.keyDown(document.body, { key: "n", ctrlKey: true });
    await tick();
    expect(mode.current).toBe("normal");
  });

  it("Ctrl-\\ followed by non-Ctrl-n does NOT leave terminal; resets prefix", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    mode.enterTerminal();
    await fireEvent.keyDown(document.body, { key: "\\", ctrlKey: true });
    await tick();
    // Non-n key (plain 'a') cancels prefix
    await fireEvent.keyDown(document.body, { key: "a" });
    await tick();
    expect(mode.current).toBe("terminal");

    // A second Ctrl-\ + non-n should also not leave
    await fireEvent.keyDown(document.body, { key: "\\", ctrlKey: true });
    await tick();
    await fireEvent.keyDown(document.body, { key: "x" });
    await tick();
    expect(mode.current).toBe("terminal");
  });

  it("in TERMINAL mode a normal key (j) does NOT change view or selection", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Alpha" });
    await tick();

    layout.setView("agent");
    mode.enterTerminal();

    const activeIdBefore = null; // activeId starts null

    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();

    // View must be unchanged
    expect(layout.view).toBe("agent");
    // No workspace became active
    expect(screen.queryByRole("button", { name: "Alpha" })?.getAttribute("aria-current")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 4.25.6c: New command-registry entries + bell-opens-hub + unread badge
// ---------------------------------------------------------------------------

describe("App.svelte 4.25.6c: session:close command", () => {
  it("dispatching session:close calls closeWorkspace(active.id) but does NOT remove the workspace from the list", async () => {
    const { listWorkspaces, closeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false, tokens: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();

    // Dispatch session:close via MenuBar: Session menu → Close session
    const sessionMenu = screen.getByRole("menuitem", { name: "Session" });
    await fireEvent.click(sessionMenu);
    await tick();
    const closeItem = screen.getByRole("menuitem", { name: "Close session" });
    await fireEvent.click(closeItem);
    await tick();

    expect(closeWorkspace).toHaveBeenCalledWith("ws-1");
    // Workspace must still be in the sidebar list
    expect(screen.getByRole("button", { name: "Alpha" })).toBeInTheDocument();
  });
});

describe("App.svelte 4.25.6c: worktree:open command", () => {
  it("dispatching worktree:open calls openWorkspace(active.id)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false, tokens: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    await tick();
    vi.mocked(openWorkspace).mockClear();

    // Dispatch via Worktree menu → Open worktree
    const worktreeMenu = screen.getByRole("menuitem", { name: "Worktree" });
    await fireEvent.click(worktreeMenu);
    await tick();
    const openItem = screen.getByRole("menuitem", { name: "Open worktree" });
    await fireEvent.click(openItem);
    await tick();

    expect(openWorkspace).toHaveBeenCalledWith("ws-1");
  });
});

describe("App.svelte 4.25.6c: agent:approve-all / deny-all", () => {
  const twoApprovalWorkspaces = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "awaiting-approval" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: true, attention: false, tokens: false },
    },
    {
      id: "ws-2", title: "Beta", branch: "feat/beta", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      caps: { approvals: true, attention: false, tokens: false },
    },
  ];

  it("agent:approve-all calls approve(reqId,'allow') for every pending approval and clears them", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: "Alpha" });

    // Inject approval events for both workspaces
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-a1", tool: "bash", summary: "Alpha approval" } });
    cb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-b1", tool: "bash", summary: "Beta approval" } });
    await tick();

    // Dispatch agent:approve-all via Agent menu
    const agentMenu = screen.getByRole("menuitem", { name: "Agent" });
    await fireEvent.click(agentMenu);
    await tick();
    const approveAllItem = screen.getByRole("menuitem", { name: "Approve all pending" });
    await fireEvent.click(approveAllItem);

    await waitFor(() => {
      expect(approve).toHaveBeenCalledWith("req-a1", "allow");
      expect(approve).toHaveBeenCalledWith("req-b1", "allow");
    });

    // Switch to Alpha (ws-1) and verify its approval was cleared (summary absent)
    await fireEvent.click(screen.getByRole("button", { name: "Alpha" }));
    await waitFor(() =>
      expect(screen.queryByText("Alpha approval")).not.toBeInTheDocument()
    );

    // Switch to Beta (ws-2) and verify its approval was also cleared
    await fireEvent.click(screen.getByRole("button", { name: "Beta" }));
    await waitFor(() =>
      expect(screen.queryByText("Beta approval")).not.toBeInTheDocument()
    );
  });

  it("agent:approve-all partial failure: failed approval stays; a blocking notification added", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWorkspaces);
    // First call rejects (ws-1), second succeeds (ws-2)
    vi.mocked(approve).mockRejectedValueOnce(new Error("network error"));
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: "Alpha" });

    const { getItems } = await import("./lib/stores/notifications.svelte");
    const notifBefore = getItems().length;

    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-fail", tool: "bash", summary: "Will fail" } });
    cb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-ok", tool: "bash", summary: "Will pass" } });
    await tick();

    // Dispatch approve-all
    const agentMenu = screen.getByRole("menuitem", { name: "Agent" });
    await fireEvent.click(agentMenu);
    await tick();
    const approveAllItem = screen.getByRole("menuitem", { name: "Approve all pending" });
    await fireEvent.click(approveAllItem);

    // Wait for the async command to settle
    await waitFor(() => {
      const items = getItems();
      expect(items.some(n => n.title === "Approval failed")).toBe(true);
    });

    // A blocking notification was added for the failure
    const items = getItems();
    expect(items.length).toBeGreaterThan(notifBefore);
    expect(items.some(n => n.title === "Approval failed")).toBe(true);

    // ws-1 (req-fail) was NOT cleared — select Alpha and verify "Will fail" summary still present
    await fireEvent.click(screen.getByRole("button", { name: "Alpha" }));
    await waitFor(() =>
      expect(screen.getByText("Will fail")).toBeInTheDocument()
    );

    // ws-2 (req-ok) WAS cleared — select Beta and verify "Will pass" summary is absent
    await fireEvent.click(screen.getByRole("button", { name: "Beta" }));
    await waitFor(() =>
      expect(screen.queryByText("Will pass")).not.toBeInTheDocument()
    );
  });
});

describe("App.svelte 4.25.6c: notifications:open toggles hub", () => {
  it("hub not in DOM initially; clicking bell shows it; clicking again hides it", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // Hub must NOT be in DOM initially
    expect(screen.queryByRole("region", { name: "notification hub" })).not.toBeInTheDocument();

    // Click the bell → hub opens
    const bell = screen.getByRole("button", { name: "notifications" });
    await fireEvent.click(bell);
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "notification hub" })).toBeInTheDocument()
    );

    // Click again → hub closes
    await fireEvent.click(bell);
    await tick();
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "notification hub" })).not.toBeInTheDocument()
    );
  });
});

describe("App.svelte 4.25.6c: unread badge on MenuBar bell", () => {
  it("MenuBar badge reflects the count of unread notifications from the store", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // Capture current rendered badge count (store may have items from earlier tests)
    const { getItems } = await import("./lib/stores/notifications.svelte");
    const unreadBefore = getItems().filter(n => !n.read).length;

    // Inject a blocking notification via the notify callback (unread by default)
    const cb = captured.notify.at(-1)!;
    cb({ tier: "blocking", title: "Badge test", body: "body", workspaceId: "ws-1" });
    await tick();

    // The badge must now show unreadBefore+1
    const expectedCount = unreadBefore + 1;
    await waitFor(() => {
      const badge = document.querySelector(".badge");
      expect(badge).toBeInTheDocument();
      expect(badge!.textContent).toBe(String(expectedCount));
    });
  });
});

// ---------------------------------------------------------------------------
// OS notification focus wiring
// ---------------------------------------------------------------------------

describe("App.svelte: SetWindowFocus wiring on focus/blur", () => {
  it("calls setWindowFocus(true) on window focus and setWindowFocus(false) on window blur", async () => {
    const { setWindowFocus, listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    const sfMock = setWindowFocus as ReturnType<typeof vi.fn>;
    // Clear any calls made during mount (initial hasFocus report).
    sfMock.mockClear();

    // Simulate the window losing focus (blur).
    window.dispatchEvent(new Event("blur"));
    await tick();
    expect(sfMock).toHaveBeenCalledWith(false);

    sfMock.mockClear();

    // Simulate the window gaining focus.
    window.dispatchEvent(new Event("focus"));
    await tick();
    expect(sfMock).toHaveBeenCalledWith(true);
  });
});

// ---------------------------------------------------------------------------
// 4.25.6d: HelpDialog — opened by help:shortcuts / help:about menu commands
// ---------------------------------------------------------------------------

describe("App.svelte 4.25.6d: HelpDialog opens via help:shortcuts command", () => {
  it("dispatching help:shortcuts via the Help menu opens HelpDialog; closing it hides it", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // HelpDialog must not be present initially
    expect(screen.queryByRole("dialog", { name: "help" })).not.toBeInTheDocument();

    // Open the Help menu and click "Keyboard shortcuts"
    const helpMenu = screen.getByRole("menuitem", { name: "Help" });
    await fireEvent.click(helpMenu);
    await tick();
    const shortcutsItem = screen.getByRole("menuitem", { name: "Keyboard shortcuts" });
    await fireEvent.click(shortcutsItem);
    await tick();

    // HelpDialog must now be visible
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "help" })).toBeInTheDocument()
    );

    // Close via the close button
    await fireEvent.click(screen.getByRole("button", { name: "close help" }));
    await tick();

    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "help" })).not.toBeInTheDocument()
    );
  });
});

// ---------------------------------------------------------------------------
// Feature A (SPEC §8): Approval batching — "Approve all / Deny all" buttons
// ---------------------------------------------------------------------------

describe("App.svelte Feature A: approval batch buttons (SPEC §8)", () => {
  const twoApprovalWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "awaiting-approval" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: true, attention: false, tokens: false },
    },
    {
      id: "ws-2", title: "Beta", branch: "feat/beta", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      caps: { approvals: true, attention: false, tokens: false },
    },
  ];

  it("with TWO pending approvals: batch buttons render, clicking Approve all calls approve for both", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Inject approval events for both workspaces
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-a", tool: "bash", summary: "Alpha task" } });
    cb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-b", tool: "bash", summary: "Beta task" } });
    await tick();

    // Select Alpha so the approval card appears (active.id = ws-1, approvals[ws-1] exists)
    await fireEvent.click(alphaBtn);
    await tick();

    // With two pending approvals (approvalQueue.length === 2), batch buttons must render
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /approve all/i })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /deny all/i })).toBeInTheDocument();
    });

    // Click Approve all → decideAll("allow") → approve called for both req IDs
    await fireEvent.click(screen.getByRole("button", { name: /approve all/i }));
    await waitFor(() => {
      expect(approve).toHaveBeenCalledWith("req-a", "allow");
      expect(approve).toHaveBeenCalledWith("req-b", "allow");
    });
  });

  it("with ONE pending approval: batch buttons do NOT render", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([twoApprovalWs[0]]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-solo", tool: "bash", summary: "Solo task" } });
    await tick();
    await fireEvent.click(alphaBtn);
    await tick();

    // Card must render (Allow button visible)
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Allow" })).toBeInTheDocument()
    );

    // Batch buttons must NOT be present for a single-item queue
    expect(screen.queryByRole("button", { name: /approve all/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /deny all/i })).not.toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Feature B (SPEC §7.3/§7.7): selection→agent via onSendToAgent
// ---------------------------------------------------------------------------

describe("App.svelte Feature B: sendToAgent wires Editor→writeToPty (SPEC §7.3/§7.7)", () => {
  const codeWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: false, attention: false, tokens: false },
    },
  ];

  it("Editor's onSendToAgent calls writeToPty with active paneId and UTF-8 encoded text", async () => {
    const { listWorkspaces, writeToPty } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(codeWs);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha and switch to code view so EditorProbe mounts
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    layout.setView("code");
    await tick();

    // EditorProbe (our custom mock) renders a "send to agent" button
    await waitFor(() =>
      expect(screen.getByTestId("editor")).toBeInTheDocument()
    );

    const sendBtn = screen.getByRole("button", { name: "send to agent" });
    await fireEvent.click(sendBtn);
    await tick();

    // writeToPty must have been called with the active paneId and UTF-8 bytes of "hello from editor"
    await waitFor(() => {
      expect(writeToPty).toHaveBeenCalled();
      const calls = (writeToPty as ReturnType<typeof vi.fn>).mock.calls;
      const call = calls.find(([paneId]) => paneId === "p1");
      expect(call).toBeDefined();
      const [, bytes] = call!;
      const decoded = new TextDecoder().decode(new Uint8Array(bytes));
      expect(decoded).toBe("hello from editor");
    });
  });

  it("sendToAgent does nothing when no workspace is active (no paneId)", async () => {
    const { listWorkspaces, writeToPty } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(codeWs);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // DON'T select any workspace — no active session
    await screen.findByRole("button", { name: "Alpha" });
    layout.setView("code");
    await tick();

    // No editor visible (no active workspace)
    expect(screen.queryByTestId("editor")).not.toBeInTheDocument();

    // If sendToAgent were called, writeToPty must NOT have been called
    // Verify by directly checking _editorSendToAgent is not set (editor not mounted)
    expect(screen.queryByRole("button", { name: "send to agent" })).not.toBeInTheDocument();
    expect(writeToPty).not.toHaveBeenCalled();
  });
});
