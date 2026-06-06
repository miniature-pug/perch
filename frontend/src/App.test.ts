// frontend/src/App.test.ts
import { render, screen, fireEvent, waitFor, within } from "@testing-library/svelte";
import { tick } from "svelte";
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

// Stub ShellDrawer (imports xterm which crashes jsdom). Use a probe stub that
// surfaces its paneId/cwd props so the BUG-1b regression test can assert the
// shell drawer is wired with a safe pane key (no colon).
vi.mock("./lib/ShellDrawer.svelte", async () => ({
  default: (await import("./lib/__stubs__/ShellDrawerProbe.svelte")).default,
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
  agent:           [] as Array<(ev: any) => void>,
  notify:          [] as Array<(n: any)  => void>,
  fsChanged:       [] as Array<(p: any)  => void>,
  workspaceAttach: [] as Array<(p: any)  => void>,
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
  createWorkspace: vi.fn(async (_agent: string, _repo: string, _baseRef: string, _branch: string, _worktree: boolean) => ({
    id: "ws-new", title: "New", branch: "main", state: "idle",
    worktreePath: "/tmp/new", agent: "claude", paneId: "p-new", lastActive: "",
    caps: { approvals: false, attention: false },
  })),
  workspaceForBranch: vi.fn(async (_repoPath: string, _branch: string) => ({ id: "", found: false })),
  removeWorkspace: vi.fn(async () => {}),
  writeToPty:      vi.fn(async () => {}),
  branches:        vi.fn(async (_repo: string) => ["main", "feat/x"]),
  discoverRepos:   vi.fn(async () => [
    { path: "/discovered/repo-a", name: "repo-a", branch: "main", worktrees: [] },
  ]),
  onAgentEvent:    vi.fn((cb) => { captured.agent.push(cb);     return () => {}; }),
  onNotify:        vi.fn((cb) => { captured.notify.push(cb);    return () => {}; }),
  onFsChanged:     vi.fn((cb) => { captured.fsChanged.push(cb); return () => {}; }),
  onWorkspaceAttach: vi.fn((cb) => { captured.workspaceAttach.push(cb); return () => {}; }),
  diffStat:        vi.fn(async (worktreePath: string) => {
    // Return 2 files summing to +5 −2 for /tmp/alpha; empty for all others.
    // This keeps existing tests unaffected (they don't assert on diffstat values)
    // while letting Feature 1 tests verify a known non-zero total.
    if (worktreePath === "/tmp/alpha") {
      return [
        { path: "a.ts", added: 3, removed: 1, status: "M" as const },
        { path: "b.ts", added: 2, removed: 1, status: "M" as const },
      ];
    }
    return [];
  }),
  setWindowFocus:        vi.fn(async () => {}),
  forceRemoveWorkspace:  vi.fn(async () => {}),
  listStaleSessions:     vi.fn(async () => []),
  cleanupSessions:       vi.fn(async () => {}),
  homeShellCwd:          vi.fn(async () => "/home/user"),
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
    caps: { approvals: false, attention: false },
  },
  {
    id: "ws-2", title: "Beta", branch: "feat/beta", state: "running" as const,
    worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
    caps: { approvals: false, attention: false },
  },
];

beforeEach(async () => {
  vi.clearAllMocks();
  // Reset captured callback arrays (vi.clearAllMocks does NOT empty them).
  captured.agent.length           = 0;
  captured.notify.length          = 0;
  captured.fsChanged.length       = 0;
  captured.workspaceAttach.length = 0;
  // Reset the real layout singleton to default values before each test.
  const { layout } = await import("./lib/stores/layout.svelte");
  layout.setView("agent");
  layout.split = false as any;
  (layout as any).splitId   = null;
  (layout as any).sidebarW  = 240;
  (layout as any).shellH    = 200;
  (layout as any).collapsed = {};
  (layout as any).order     = [];
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

  it("selecting a workspace shows resume preview then calls openWorkspace(id) after confirm", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Preview modal appears; openWorkspace not yet called
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();
    // Confirm via the Open button
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
    // Preview gone; workspace now active
    await waitFor(() =>
      expect(alphaBtn).toHaveAttribute("aria-current", "page")
    );
    expect(screen.getByRole("button", { name: "Beta" })).not.toHaveAttribute("aria-current");
  });

  it("clicking a sidebar row shows a resume preview before opening workspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => screen.getByText("Alpha"));
    await fireEvent.click(screen.getByRole("button", { name: /Beta/i }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();
    const confirmBtn = screen.getByRole("button", { name: /^open$/i });
    await fireEvent.click(confirmBtn);
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-2"));
  });

  it("resume preview Cancel button dismisses without opening workspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => screen.getByText("Alpha"));
    await fireEvent.click(screen.getByRole("button", { name: /Alpha/i }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    await waitFor(() => expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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

  it("split mode: view='agent' + split=true + splitId set → two independent TerminalProbes with distinct paneIds", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for workspaces to load (restore() has run by now)
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    // Set agent view, enable split, and assign splitId to the second workspace (ws-2)
    layout.setView("agent");
    layout.toggleSplit(); // false → true
    layout.setSplitId("ws-2");
    await tick();
    // Both primary and secondary slots should have a TerminalProbe
    await waitFor(() => {
      const terminals = screen.getAllByTestId("terminal");
      expect(terminals).toHaveLength(2);
    });
    // Primary pane shows Alpha (paneId=p1), secondary pane shows Beta (paneId=p2)
    const primaryPane   = document.querySelector("[data-pane='primary']") as HTMLElement;
    const secondaryPane = document.querySelector("[data-pane='secondary']") as HTMLElement;
    expect(within(primaryPane).getByTestId("terminal").dataset.paneId).toBe("p1");
    expect(within(secondaryPane).getByTestId("terminal").dataset.paneId).toBe("p2");
  });

  // BUG-1b regression: the shell drawer pane key must be a safe shape. It was
  // "{wsid}:shell" — the colon is rejected by Go's validateSessionID charset
  // [A-Za-z0-9_-], so OpenShell rejected the id and the drawer never connected to
  // a pty. It is now "shell-{wsid}". Assert the rendered paneId starts with
  // "shell-" and contains no colon / out-of-charset character.
  it("shell drawer paneId is the safe 'shell-{wsid}' shape (no colon)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // ShellDrawer renders under {#if active}, so select a workspace first.
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const drawer = await screen.findByTestId("shell-drawer-probe");
    const paneId = drawer.dataset.paneId!;
    expect(paneId).toBe("shell-ws-1");
    expect(paneId.startsWith("shell-")).toBe(true);
    // Mirror Go's validateSessionID charset: no colon, no other invalid chars.
    expect(paneId).toMatch(/^[A-Za-z0-9_-]+$/);
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

  it("onAgentEvent: a 'question' event flips state to awaiting-input and shows NO approval card (signal-only)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha so it is the active workspace (otherwise "no dock" is vacuous).
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Fire a question event: kind "question", no approval payload.
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "question", state: "awaiting-input" });
    await tick();

    // Sidebar maps "awaiting-input" → "asking you".
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Alpha" })).toHaveTextContent("asking you")
    );

    // A question is signal-only: NO approval dock, NO Allow button.
    expect(document.querySelector("[data-zone='approval-dock']")).toBeNull();
    expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument();
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
      caps: { approvals: true, attention: false },
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
// 4.25.6a: Dialogs + DragDrop
// ---------------------------------------------------------------------------

describe("App.svelte NewSessionDialog (4.25.6a)", () => {
  it("Sidebar onNew / openNewSession opens the dialog; submitting calls createWorkspace and refreshes", async () => {
    const { listWorkspaces, createWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        {
          id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
          worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
          caps: { approvals: false, attention: false },
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

    // Wait for "starting point" select options to load (confirms branches loaded from mock)
    await waitFor(() => {
      const startingPointSelect = screen.getByLabelText(/starting point/i) as HTMLSelectElement;
      expect(startingPointSelect.options.length).toBeGreaterThan(0);
    });

    // Select repo — the new dialog defaults to worktree=true, new-branch mode.
    // baseRef will be "main" (first branch from the mock). Set branch name manually.
    await fireEvent.change(screen.getByLabelText(/^repo$/i), { target: { value: "/tmp/alpha" } });
    // Wait for branches to load for new repo
    await waitFor(() => {
      const startingPointSelect = screen.getByLabelText(/starting point/i) as HTMLSelectElement;
      expect(startingPointSelect.options.length).toBeGreaterThan(0);
    });
    // Set branch name via the text input (new-branch mode, aria-label="branch name")
    await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/x" } });

    // Hit the "Create" button inside the dialog
    const createBtn = screen.getByRole("button", { name: "Create" });
    await fireEvent.click(createBtn);
    await tick();

    // createWorkspace must have been called with the 5-arg signature:
    // agent="claude", repo="/tmp/alpha", baseRef="main" (first branch from mock), branch="feat/x", worktree=true
    expect(createWorkspace).toHaveBeenCalledWith("claude", "/tmp/alpha", "main", expect.stringMatching(/^[A-Za-z0-9._\/-]+$/), true);

    // Dialog must close
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "new session" })).not.toBeInTheDocument()
    );
  });

  it("handleCreate surfaces ErrWorktreeDirty as a blocking notification and keeps dialog open", async () => {
    const { listWorkspaces, createWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    // Simulate the Go backend returning ErrWorktreeDirty (wrapped by fmt.Errorf).
    // The real string is "checkout branch: worktree has uncommitted changes".
    vi.mocked(createWorkspace).mockRejectedValueOnce(
      new Error("checkout branch: worktree has uncommitted changes")
    );

    const { getItems } = await import("./lib/stores/notifications.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: "Alpha" });
    const notifBefore = getItems().length;

    // Open dialog
    const newBtn = screen.getByRole("button", { name: "New session" });
    await fireEvent.click(newBtn);
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );

    // Wait for starting point branches to load
    await waitFor(() => {
      const sp = screen.getByLabelText(/starting point/i) as HTMLSelectElement;
      expect(sp.options.length).toBeGreaterThan(0);
    });

    // Hit Create — createWorkspace rejects with dirty-tree error
    const createBtn = screen.getByRole("button", { name: "Create" });
    await fireEvent.click(createBtn);
    await tick();

    // A blocking notification must appear with a clean-tree message
    await waitFor(() => {
      const items = getItems();
      expect(items.length).toBeGreaterThan(notifBefore);
      const dirty = items.find(n => n.title === "Cannot switch branch");
      expect(dirty).toBeDefined();
    });

    // Dialog must STAY open so the user can correct their choice
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
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

  it("handleCreate calls onSelect with existing session id when WorkspaceForBranch returns found=true", async () => {
    const { listWorkspaces, createWorkspace, workspaceForBranch } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-existing", title: "Existing", branch: "feat/taken", state: "idle" as const,
        worktreePath: "/tmp/existing", agent: "claude", paneId: "p-existing", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    (workspaceForBranch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ id: "ws-existing", found: true });

    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Existing" });

    // Open dialog
    await fireEvent.click(screen.getByRole("button", { name: "New session" }));
    await waitFor(() => screen.getByRole("dialog", { name: "new session" }));

    // Wait for branch options (starting point) to load
    await waitFor(() => screen.getByLabelText(/starting point/i));

    // Set branch name to the taken branch name
    await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/taken" } });

    // Create — should trigger resume, not a new workspace
    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await tick();

    // workspaceForBranch was called
    expect(workspaceForBranch).toHaveBeenCalledWith(expect.any(String), "feat/taken");

    // createWorkspace was NOT called — resumed instead
    expect(createWorkspace).not.toHaveBeenCalled();

    // Dialog closed
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "new session" })).not.toBeInTheDocument()
    );

    // Resume preview appears for the existing session — confirm to open it
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
  });
});

describe("App.svelte ConfirmDialog (workspace remove) (4.25.6a)", () => {
  it("session:remove command shows ConfirmDialog; confirming hides workspace + shows undo toast (no immediate removeWorkspace)", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select ws-1 to make it active (required for session:remove to find active)
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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

    // Dialog closes immediately
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "confirm" })).not.toBeInTheDocument()
    );

    // Workspace disappears from sidebar immediately (optimistic hide)
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Alpha" })).not.toBeInTheDocument()
    );

    // Undo toast appears
    await waitFor(() =>
      expect(screen.getByTestId("undo-toast")).toBeInTheDocument()
    );

    // removeWorkspace must NOT have been called yet
    expect(removeWorkspace).not.toHaveBeenCalled();
  });
});

describe("App.svelte DragDrop (4.25.6a)", () => {
  it("dropping a file onto the agent terminal writes @path bytes via writeToPty", async () => {
    const { listWorkspaces, writeToPty } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select ws-1 and go to agent view
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
      caps: { approvals: true, attention: false },
    },
    {
      id: "ws-2", title: "Beta", branch: "feat/beta", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      caps: { approvals: true, attention: false },
    },
  ];

  // SAFETY (Feature A): "Approve all pending" must scope to the ACTIVE workspace
  // ONLY — it must NEVER silently green-light a tool waiting in a different,
  // unseen workspace. With Alpha active, approve-all resolves Alpha's request and
  // leaves Beta's untouched.
  it("agent:approve-all resolves ONLY the active workspace's approval; other workspace stays pending", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha (ws-1) so it is the ACTIVE workspace.
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

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

    // Only Alpha's (active) request is approved.
    await waitFor(() => {
      expect(approve).toHaveBeenCalledWith("req-a1", "allow");
    });
    // Beta's request must NOT have been touched — the safety invariant.
    expect(approve).not.toHaveBeenCalledWith("req-b1", "allow");

    // Alpha (active) approval cleared.
    await waitFor(() =>
      expect(screen.queryByText("Alpha approval")).not.toBeInTheDocument()
    );

    // Beta still has its pending approval — switch to it and verify it survived.
    await fireEvent.click(screen.getByRole("button", { name: "Beta" }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() =>
      expect(screen.getByText("Beta approval")).toBeInTheDocument()
    );
  });

  it("agent:approve-all failure on the active approval: it stays; a blocking notification added", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWorkspaces);
    // The active workspace's approve call rejects.
    vi.mocked(approve).mockRejectedValueOnce(new Error("network error"));
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha (ws-1) so it is the ACTIVE workspace.
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const { getItems } = await import("./lib/stores/notifications.svelte");
    const notifBefore = getItems().length;

    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-fail", tool: "bash", summary: "Will fail" } });
    cb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-ok", tool: "bash", summary: "Untouched" } });
    await tick();

    // Dispatch approve-all
    const agentMenu = screen.getByRole("menuitem", { name: "Agent" });
    await fireEvent.click(agentMenu);
    await tick();
    const approveAllItem = screen.getByRole("menuitem", { name: "Approve all pending" });
    await fireEvent.click(approveAllItem);

    // A blocking notification was added for the failure
    await waitFor(() => {
      const items = getItems();
      expect(items.some(n => n.title === "Approval failed")).toBe(true);
    });
    expect(getItems().length).toBeGreaterThan(notifBefore);

    // ws-1 (req-fail, active) was NOT cleared — "Will fail" summary still present
    await waitFor(() =>
      expect(screen.getByText("Will fail")).toBeInTheDocument()
    );

    // ws-2 (req-ok) was never acted on — switch to Beta and verify it is still pending
    await fireEvent.click(screen.getByRole("button", { name: "Beta" }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() =>
      expect(screen.getByText("Untouched")).toBeInTheDocument()
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
      caps: { approvals: true, attention: false },
    },
    {
      id: "ws-2", title: "Beta", branch: "feat/beta", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      caps: { approvals: true, attention: false },
    },
  ];

  it("with TWO pending approvals: batch buttons render (cross-workspace count), but Approve all resolves ONLY the active workspace", async () => {
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // The cross-workspace queue (2 pending) still DRIVES the batch-button render —
    // the "N pending" indicator is preserved.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /approve all/i })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /deny all/i })).toBeInTheDocument();
    });

    // SAFETY: clicking Approve all resolves ONLY Alpha's (active) request. Beta's
    // request — waiting in an unseen workspace — must NOT be silently approved.
    await fireEvent.click(screen.getByRole("button", { name: /approve all/i }));
    await waitFor(() => {
      expect(approve).toHaveBeenCalledWith("req-a", "allow");
    });
    expect(approve).not.toHaveBeenCalledWith("req-b", "allow");

    // Beta's approval survives — switch to it and confirm it is still pending.
    await fireEvent.click(screen.getByRole("button", { name: "Beta" }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() =>
      expect(screen.getByText("Beta task")).toBeInTheDocument()
    );
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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
      caps: { approvals: false, attention: false },
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
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
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

// ---------------------------------------------------------------------------
// FEATURE 1 (SPEC §7.7): Repo discovery + first-run empty state
// ---------------------------------------------------------------------------

describe("App.svelte Feature 1: discoverRepos called on dialog open; discovered repos appear in dialog", () => {
  it("opening the New Session dialog calls discoverRepos and discovered path appears as an option", async () => {
    const { listWorkspaces, discoverRepos } = await import("./lib/wails");
    // Fresh install — no existing workspaces
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    (discoverRepos as ReturnType<typeof vi.fn>).mockResolvedValue([
      { path: "/discovered/my-repo", name: "my-repo", branch: "main", worktrees: [] },
    ]);

    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // discoverRepos must NOT have been called yet (dialog not open)
    expect(discoverRepos).not.toHaveBeenCalled();

    // Open the New Session dialog via the Sidebar button
    const newBtn = screen.getByRole("button", { name: "New session" });
    await fireEvent.click(newBtn);
    await tick();

    // Dialog opens
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );

    // discoverRepos must have been called when dialog opened
    expect(discoverRepos).toHaveBeenCalled();

    // The discovered repo path must appear as an option in the Repo select
    await waitFor(() => {
      const repoSelect = screen.getByLabelText(/repo/i) as HTMLSelectElement;
      const optionValues = Array.from(repoSelect.options).map(o => o.value);
      expect(optionValues).toContain("/discovered/my-repo");
    });
  });

  it("first-run empty state (no workspaces) renders a 'New Session' CTA button", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // Wait for initial load
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Alpha" })).not.toBeInTheDocument()
    );

    // The empty state must be present
    const emptyState = document.querySelector("[data-testid='empty-state']");
    expect(emptyState).toBeInTheDocument();

    // A "New Session" primary button must be in the empty state
    const newSessionBtn = within(emptyState as HTMLElement).getByRole("button", { name: "New Session" });
    expect(newSessionBtn).toBeInTheDocument();
  });

  it("clicking the empty-state 'New Session' button opens the New Session dialog", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    const emptyState = await screen.findByTestId("empty-state");
    const newSessionBtn = within(emptyState).getByRole("button", { name: "New Session" });
    await fireEvent.click(newSessionBtn);
    await tick();

    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );
  });

  it("clicking a quick-start template button opens dialog and pre-selects the agent", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);

    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    const emptyState = await screen.findByTestId("empty-state");
    const opencodeBtn = within(emptyState).getByRole("button", { name: "Opencode session" });
    await fireEvent.click(opencodeBtn);
    await tick();

    // Dialog must open
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );

    // Agent select must be pre-set to "opencode"
    await waitFor(() => {
      const agentSelect = screen.getByLabelText(/agent/i) as HTMLSelectElement;
      expect(agentSelect.value).toBe("opencode");
    });
  });
});

// ---------------------------------------------------------------------------
// FEATURE 2 (SPEC §8): Deferred removal + undo toast
// ---------------------------------------------------------------------------

describe("App.svelte Feature 2: deferred remove — hides workspace + shows undo toast without calling removeWorkspace", () => {
  const removeWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: false, attention: false },
    },
  ];

  it("confirming remove hides workspace from sidebar and shows undo toast; removeWorkspace NOT called", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(removeWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Trigger remove via Sidebar context: open command palette → session:remove
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("option", { name: /remove session/i }));
    await tick();

    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "confirm" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await tick();

    // Workspace hidden immediately
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Alpha" })).not.toBeInTheDocument()
    );

    // Undo toast visible
    await waitFor(() =>
      expect(screen.getByTestId("undo-toast")).toBeInTheDocument()
    );

    // removeWorkspace NOT called yet
    expect(removeWorkspace).not.toHaveBeenCalled();
  });

  it("clicking Undo restores the workspace and removeWorkspace is never called", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(removeWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Trigger remove
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("option", { name: /remove session/i }));
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "confirm" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await tick();

    // Toast appears
    const toast = await screen.findByTestId("undo-toast");
    expect(toast).toBeInTheDocument();

    // Click Undo
    const undoBtn = within(toast).getByRole("button", { name: "Undo" });
    await fireEvent.click(undoBtn);
    await tick();

    // Workspace restored in sidebar
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Alpha" })).toBeInTheDocument()
    );

    // Toast gone
    await waitFor(() =>
      expect(screen.queryByTestId("undo-toast")).not.toBeInTheDocument()
    );

    // removeWorkspace must NEVER have been called
    expect(removeWorkspace).not.toHaveBeenCalled();
  });

  it("undo toast disappears after the undo action and removeWorkspace is never called", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(removeWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("option", { name: /remove session/i }));
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "confirm" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await tick();

    const toast = await screen.findByTestId("undo-toast");
    const undoBtn = within(toast).getByRole("button", { name: "Undo" });
    await fireEvent.click(undoBtn);
    await tick();

    await waitFor(() =>
      expect(screen.queryByTestId("undo-toast")).not.toBeInTheDocument()
    );
    expect(removeWorkspace).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// SPEC §7.2: independent secondary pane (splitId)
// ---------------------------------------------------------------------------

describe("App.svelte §7.2 split secondary pane", () => {
  it("split=true + splitId=ws-2 → secondary pane shows Beta terminal (paneId p2), primary shows Alpha (paneId p1)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha as active
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("agent");
    layout.toggleSplit();
    layout.setSplitId("ws-2");
    await tick();

    await waitFor(() => {
      const primaryPane   = document.querySelector("[data-pane='primary']") as HTMLElement;
      const secondaryPane = document.querySelector("[data-pane='secondary']") as HTMLElement;
      expect(within(primaryPane).getByTestId("terminal").dataset.paneId).toBe("p1");
      expect(within(secondaryPane).getByTestId("terminal").dataset.paneId).toBe("p2");
    });
  });

  it("split=true + splitId=null → secondary pane renders the session-picker placeholder, no terminal in secondary", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha as active
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("agent");
    layout.toggleSplit(); // splitId remains null
    await tick();

    await waitFor(() => {
      const secondaryPane = document.querySelector("[data-pane='secondary']") as HTMLElement;
      expect(secondaryPane).toBeInTheDocument();
      // Placeholder shown
      expect(within(secondaryPane).getByTestId("split-picker")).toBeInTheDocument();
      // No terminal in secondary
      expect(within(secondaryPane).queryByTestId("terminal")).not.toBeInTheDocument();
    });
  });

  it("split=true + splitId=null → session-picker lists other workspaces (Beta, not Alpha)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha as active
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("agent");
    layout.toggleSplit();
    await tick();

    await waitFor(() => {
      const select = screen.getByRole("combobox", { name: "secondary session" }) as HTMLSelectElement;
      const optionTexts = Array.from(select.options).map(o => o.text);
      expect(optionTexts).toContain("Beta");
      expect(optionTexts).not.toContain("Alpha");
    });
  });
});

// --- Behavior 5: session→split via stage-level drop ---
describe("App.svelte drag-to-split (behavior 5)", () => {
  it("dropping a session id onto the stage sets layout.split=true and layout.splitId to that id", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setSplitId");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Select a workspace so the stage is active
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("agent");
    await tick();

    // Find the stage-zone drop target
    const stageZone = document.querySelector("[data-zone='stage']") as HTMLElement;
    expect(stageZone).toBeTruthy();

    // Simulate a session drop onto the stage
    const store = new Map<string, string>([["application/x-perch-session", "ws-2"]]);
    const dt = {
      setData: vi.fn(),
      getData: (type: string) => store.get(type) ?? "",
      types: ["application/x-perch-session"],
      files: [],
      effectAllowed: "move" as string,
      dropEffect: "none" as string,
    };
    await fireEvent.dragOver(stageZone, { dataTransfer: dt });
    await fireEvent.drop(stageZone, { dataTransfer: dt });

    await waitFor(() => {
      expect(layout.split).toBe(true);
      expect(spy).toHaveBeenCalledWith("ws-2");
    });
  });

  it("dropping a text payload onto the stage does NOT set split (wrong MIME)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setSplitId");
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open (even though no workspace content is needed here,
    // the click goes through the preview flow to be consistent)
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const stageZone = document.querySelector("[data-zone='stage']") as HTMLElement;
    const store = new Map<string, string>([["application/x-perch-text", "@/foo.go "]]);
    const dt = {
      setData: vi.fn(),
      getData: (type: string) => store.get(type) ?? "",
      types: ["application/x-perch-text"],
      files: [],
      effectAllowed: "copy" as string,
      dropEffect: "none" as string,
    };
    await fireEvent.drop(stageZone, { dataTransfer: dt });
    await new Promise(r => setTimeout(r, 50));
    expect(layout.split).toBe(false);
    expect(spy).not.toHaveBeenCalled();
  });
});

// --- Behavior 4b: handleReorder wired in App ---
describe("App.svelte session reorder (behavior 4b)", () => {
  it("onReorder callback from Sidebar updates layout.order", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setOrder");
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: "Alpha" });

    // Simulate dragging ws-2 onto ws-1 in the Sidebar
    // The workspace list rows are <li draggable> elements
    const betaBtn  = screen.getByRole("button", { name: "Beta" });
    const betaLi   = betaBtn.closest("li") as HTMLElement;
    const alphaBtn = screen.getByRole("button", { name: "Alpha" });
    const alphaLi  = alphaBtn.closest("li") as HTMLElement;

    const store = new Map<string, string>([["application/x-perch-session", "ws-2"]]);
    const dt = {
      setData: vi.fn(),
      getData: (type: string) => store.get(type) ?? "",
      types: ["application/x-perch-session"],
      files: [],
      effectAllowed: "move" as string,
      dropEffect: "none" as string,
    };
    await fireEvent.dragStart(betaLi, { dataTransfer: dt });
    await fireEvent.dragOver(alphaLi, { dataTransfer: dt });
    await fireEvent.drop(alphaLi, { dataTransfer: dt });

    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith(expect.arrayContaining(["ws-2"]))
    );
  });
});

// ---------------------------------------------------------------------------
// H-8: FileTree "@mention:" prefix routed to sendToAgent (not codePath)
// ---------------------------------------------------------------------------
describe("App.svelte H-8: FileTree @mention prefix routes to sendToAgent", () => {
  const codeWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: false, attention: false },
    },
  ];

  it("FileTree onOpen with '@mention:/some/file.ts' sends '@/some/file.ts ' via writeToPty, not readFile", async () => {
    const { listWorkspaces, writeToPty, readFile } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(codeWs);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();

    // Click the "@mention" button that FileTreeProbe exposes.
    // It calls onOpen("@mention:/some/file.ts") which App routes to sendToAgent.
    vi.mocked(writeToPty).mockClear();
    vi.mocked(readFile).mockClear();
    const mentionBtn = screen.getByRole("button", { name: "mention file" });
    await fireEvent.click(mentionBtn);
    await tick();

    // @mention path → writeToPty with "@/some/file.ts " (leading '@', trailing space)
    await waitFor(() => {
      expect(writeToPty).toHaveBeenCalledTimes(1);
      const [paneId, bytes] = vi.mocked(writeToPty).mock.calls[0];
      expect(paneId).toBe("p1");
      expect(new TextDecoder().decode(new Uint8Array(bytes as number[]))).toBe("@/some/file.ts ");
    });

    // readFile must NOT be called — @mention does not set codePath
    expect(readFile).not.toHaveBeenCalled();
  });

  it("FileTree onOpen without '@mention:' prefix updates codePath, does NOT call writeToPty", async () => {
    const { listWorkspaces, writeToPty } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(codeWs);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();

    vi.mocked(writeToPty).mockClear();
    const openBtn = screen.getByRole("button", { name: "open file" });
    await fireEvent.click(openBtn);
    await tick();

    // Normal path: editor receives the path
    await waitFor(() => {
      expect(screen.getByTestId("editor").dataset.path).toBe("/some/file.ts");
    });

    // writeToPty must NOT have been called for a normal path open
    expect(writeToPty).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// M-17: onDecision deletes by owning workspace, not activeId
// ---------------------------------------------------------------------------
describe("App.svelte M-17: onDecision keys deletion by reqId owner, not activeId", () => {
  it("allow on ws-2 card while active switches to ws-1 mid-await: only ws-2 cleared (race-proof)", async () => {
    // This test exercises the async race:
    //   1. Beta active → click Allow on req-ws2 → approve() deferred (won't resolve yet)
    //   2. While awaiting → click Alpha (activeId becomes ws-1)
    //   3. Resolve approve() → onDecision finishes
    //   Buggy code:  deletes approvals[activeId] = approvals["ws-1"] → Alpha task gone (wrong)
    //   Fixed code:  deletes approvals[owner("req-ws2")] = approvals["ws-2"] → Beta gone, Alpha intact
    const { listWorkspaces, approve } = await import("./lib/wails");

    // Deferred approve: caller controls when the promise resolves
    let resolveApprove!: () => void;
    vi.mocked(approve).mockImplementation(
      () => new Promise<void>((res) => { resolveApprove = res; })
    );

    const ws1 = {
      id: "ws-1", title: "Alpha", branch: "main", state: "awaiting-approval" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      caps: { approvals: true, attention: false },
    };
    const ws2 = {
      id: "ws-2", title: "Beta", branch: "feat", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      caps: { approvals: true, attention: false },
    };
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([ws1, ws2]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Inject approvals for both workspaces
    await screen.findByRole("button", { name: "Alpha" });
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-ws1", tool: "bash", summary: "Alpha task" } });
    cb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-ws2", tool: "bash", summary: "Beta task" } });
    await tick();

    // Step 1: activate Beta via preview confirm, confirm its card is visible
    const betaBtn = screen.getByRole("button", { name: "Beta" });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    await waitFor(() => expect(screen.getByText("Beta task")).toBeInTheDocument());

    // Step 2: click Allow on Beta's card → approve() called but NOT resolved yet
    const allowBtn = screen.getByRole("button", { name: "Allow" });
    await fireEvent.click(allowBtn);
    await tick();
    expect(approve).toHaveBeenCalledWith("req-ws2", "allow");

    // Step 3: switch active workspace to Alpha via preview confirm BEFORE approve resolves
    await fireEvent.click(screen.getByRole("button", { name: "Alpha" }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    // Alpha's card is now visible (activeId changed mid-await)
    await waitFor(() => expect(screen.getByText("Alpha task")).toBeInTheDocument());

    // Step 4: now resolve the approve promise
    resolveApprove();
    await tick();
    await tick(); // extra tick for promise continuation + Svelte reactivity

    // Fixed: Beta task disappears (ws-2 approval cleared by owner lookup)
    await waitFor(() => expect(screen.queryByText("Beta task")).not.toBeInTheDocument());

    // Critical: Alpha's task must still be here (activeId-keyed deletion would have removed it)
    expect(screen.getByText("Alpha task")).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// L-14: session:close cleans up approvals/fsVersion
// ---------------------------------------------------------------------------
describe("App.svelte L-14: session:close cleans up per-workspace frontend state", () => {
  it("after closeWorkspace resolves, approvals/fsVersion for that id are removed", async () => {
    const { listWorkspaces, closeWorkspace } = await import("./lib/wails");
    (closeWorkspace as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: true, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Inject an approval event for ws-1
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-cleanup", tool: "bash", summary: "Cleanup test" } });
    await tick();

    // Approval card should be visible
    await waitFor(() => expect(screen.getByText("Cleanup test")).toBeInTheDocument());

    // Close the session
    const sessionMenu = screen.getByRole("menuitem", { name: "Session" });
    await fireEvent.click(sessionMenu);
    await tick();
    const closeItem = screen.getByRole("menuitem", { name: "Close session" });
    await fireEvent.click(closeItem);
    await tick();

    // closeWorkspace must have been called
    await waitFor(() => expect(closeWorkspace).toHaveBeenCalledWith("ws-1"));

    // Approval card disappears (approvals cleaned up)
    await waitFor(() =>
      expect(screen.queryByText("Cleanup test")).not.toBeInTheDocument()
    );
  });
});

// ---------------------------------------------------------------------------
// L-21: gt/gT view cycling
// ---------------------------------------------------------------------------
describe("App.svelte L-21: g-prefix gt/gT cycles views", () => {
  beforeEach(async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
  });

  it("gt from 'agent' → 'code'", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("agent");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "t" });
    await tick();
    expect(layout.view).toBe("code");
  });

  it("gt from 'code' → 'diff'", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("code");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "t" });
    await tick();
    expect(layout.view).toBe("diff");
  });

  it("gt from 'diff' → 'agent' (wrap-around)", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("diff");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "t" });
    await tick();
    expect(layout.view).toBe("agent");
  });

  it("gT from 'agent' → 'diff' (reverse wrap-around)", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("agent");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "T" });
    await tick();
    expect(layout.view).toBe("diff");
  });

  it("gT from 'code' → 'agent'", async () => {
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    layout.setView("code");
    await fireEvent.keyDown(document.body, { key: "g" });
    await tick();
    await fireEvent.keyDown(document.body, { key: "T" });
    await tick();
    expect(layout.view).toBe("agent");
  });
});

// ---------------------------------------------------------------------------
// L-22: auto-dismiss notifications
// ---------------------------------------------------------------------------
describe("notifications.svelte.ts L-22: auto-dismiss non-blocking tiers", () => {
  it("ambient notification is marked read after ~6s (fake timers)", async () => {
    vi.useFakeTimers();
    const { addAmbient, getItems } = await import("./lib/stores/notifications.svelte");
    const before = getItems().length;

    addAmbient("ws-1", "Done", "Build succeeded");
    const items = getItems();
    expect(items.length).toBe(before + 1);
    const n = items.find((x) => x.title === "Done")!;
    expect(n).toBeDefined();
    expect(n.read).toBe(false);

    // Advance 6 seconds
    vi.advanceTimersByTime(6000);
    await tick();

    const after = getItems();
    expect(after.find((x) => x.id === n.id)?.read).toBe(true);

    vi.useRealTimers();
  });

  it("blocking notification is NEVER auto-dismissed", async () => {
    vi.useFakeTimers();
    const { addBlocking, getItems } = await import("./lib/stores/notifications.svelte");
    const before = getItems().length;

    addBlocking("ws-1", "Error", "Something broke");
    const items = getItems();
    const n = items.find((x) => x.title === "Error")!;
    expect(n).toBeDefined();

    // Advance a long time — blocking should still NOT be read
    vi.advanceTimersByTime(60000);
    await tick();

    const after = getItems();
    expect(after.find((x) => x.id === n.id)?.read).toBe(false);

    vi.useRealTimers();
  });

  it("routine notification is marked read after ~3s (fake timers)", async () => {
    vi.useFakeTimers();
    const { addRoutine, getItems } = await import("./lib/stores/notifications.svelte");
    const before = getItems().length;

    addRoutine("ws-1", "Info", "Synced");
    const items = getItems();
    const n = items.find((x) => x.title === "Info")!;
    expect(n).toBeDefined();
    expect(n.read).toBe(false);

    vi.advanceTimersByTime(3000);
    await tick();

    const after = getItems();
    expect(after.find((x) => x.id === n.id)?.read).toBe(true);

    vi.useRealTimers();
  });
});

// ---------------------------------------------------------------------------
// FEATURE 1 (§5.3): diffstat counts — Sidebar row +/− and status-line diffstat
// ---------------------------------------------------------------------------
describe("App.svelte Feature 1: diffstat counts in Sidebar and status line", () => {
  it("Sidebar row for /tmp/alpha shows +5 and −2 after workspaces load", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Wait for workspaces to load and diffStats to be computed
    await screen.findByRole("button", { name: "Alpha" });

    // diffStat for /tmp/alpha returns 2 files → +5 −2 total
    await waitFor(() => {
      const alphaBtn = screen.getByRole("button", { name: "Alpha" });
      expect(alphaBtn.textContent).toContain("+5");
      expect(alphaBtn.textContent).toContain("2");
    });
  });

  it("Sidebar row for /tmp/beta has NO diffstat span (diffStat returns [])", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: "Beta" });
    // Allow time for diffstat to settle; Beta gets [] so no span should appear
    await tick();
    await tick();

    const betaBtn = screen.getByRole("button", { name: "Beta" });
    expect(betaBtn.querySelector(".sidebar-diffstat")).toBeNull();
  });

  it("status line shows active workspace diffstat when Alpha is selected", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Status line diffstat should appear for the active workspace
    await waitFor(() => {
      const statusDiffstat = document.querySelector(".status-diffstat");
      expect(statusDiffstat).toBeInTheDocument();
      expect(statusDiffstat!.textContent).toContain("+5");
    });
  });

  it("status line shows the 'files to review' pill for the active workspace", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Alpha's diffStat returns 2 files → the goal-gradient pill "2 files to review".
    await waitFor(() => {
      const pill = document.querySelector(".status-review-pill");
      expect(pill).toBeInTheDocument();
      expect(pill!.getAttribute("aria-label")).toBe("2 files to review");
    });
  });

  it("onFsChanged for ws-1 re-calls diffStat with /tmp/alpha", async () => {
    const { listWorkspaces, diffStat } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Wait for workspaces to load (initial diffStat calls happen here)
    await screen.findByRole("button", { name: "Alpha" });
    await tick();

    // Clear call count after initial load
    vi.mocked(diffStat).mockClear();

    // Fire fs:changed for ws-1 (/tmp/alpha)
    const cb = captured.fsChanged.at(-1)!;
    cb({ workspaceId: "ws-1", path: "/tmp/alpha/changed.ts" });
    await tick();

    // diffStat must be re-called with the alpha worktreePath
    await waitFor(() =>
      expect(diffStat).toHaveBeenCalledWith("/tmp/alpha")
    );
  });
});

// ---------------------------------------------------------------------------
// FEATURE 2 (§7.7): sidebar collapse via Ctrl-b and toggle rail button
// ---------------------------------------------------------------------------
describe("App.svelte Feature 2: sidebar collapse via Ctrl-b and toggle rail", () => {
  it("Ctrl-b in NORMAL mode toggles layout.collapsed['sidebar'] from false to true", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    expect(layout.collapsed["sidebar"]).toBeFalsy();

    await fireEvent.keyDown(document.body, { key: "b", ctrlKey: true });
    await tick();

    expect(layout.collapsed["sidebar"]).toBe(true);

    // Second Ctrl-b → back to false
    await fireEvent.keyDown(document.body, { key: "b", ctrlKey: true });
    await tick();

    expect(layout.collapsed["sidebar"]).toBe(false);
  });

  it("clicking the Toggle sidebar rail button flips layout.collapsed['sidebar']", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    expect(layout.collapsed["sidebar"]).toBeFalsy();

    const railBtn = screen.getByRole("button", { name: "Toggle sidebar" });
    await fireEvent.click(railBtn);
    await tick();

    expect(layout.collapsed["sidebar"]).toBe(true);

    // aria-expanded reflects collapsed state
    expect(railBtn).toHaveAttribute("aria-expanded", "false");
  });
});

// ---------------------------------------------------------------------------
// FEATURE 4: workspace attach routing via onWorkspaceAttach
// ---------------------------------------------------------------------------
describe("App.svelte Feature 4: onWorkspaceAttach routes to matching workspace", () => {
  it("attach callback with query matching a workspace title shows resume preview for that workspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Wait for workspaces to load (onWorkspaceAttach is subscribed in onMount)
    await screen.findByRole("button", { name: "Alpha" });
    await tick();

    vi.mocked(openWorkspace).mockClear();

    // Fire the attach callback with a query that matches "Alpha" by title
    const cb = captured.workspaceAttach.at(-1)!;
    cb({ query: "Alpha" });
    await tick();

    // onSelect shows the resume preview; openWorkspace not yet called
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();
    // Confirm to open
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
  });

  it("attach callback with query matching a worktreePath substring shows resume preview for that workspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: "Beta" });
    await tick();

    vi.mocked(openWorkspace).mockClear();

    // "beta" matches ws-2's worktreePath "/tmp/beta" case-insensitively
    const cb = captured.workspaceAttach.at(-1)!;
    cb({ query: "beta" });
    await tick();

    // onSelect shows the resume preview; openWorkspace not yet called
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();
    // Confirm to open
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-2"));
  });

  it("attach callback with a query matching nothing does NOT call openWorkspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: "Alpha" });
    await tick();

    vi.mocked(openWorkspace).mockClear();

    const cb = captured.workspaceAttach.at(-1)!;
    cb({ query: "xyzzy-no-match" });
    await tick();

    expect(openWorkspace).not.toHaveBeenCalled();
    expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Stale-banner (on mount listStaleSessions)
// ---------------------------------------------------------------------------
describe("App.svelte stale-session banner", () => {
  it("shows stale banner when listStaleSessions returns sessions", async () => {
    const { listStaleSessions } = await import("./lib/wails");
    (listStaleSessions as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: "ws-old", title: "old", branch: "feat/old", agent: "claude",
        lastActive: new Date(0).toISOString(), added: 0, removed: 0,
        clean: true, merged: true, safe: true },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => expect(screen.getByTestId("stale-banner")).toBeInTheDocument());
    expect(screen.getByTestId("stale-banner")).toHaveTextContent(/1 session/i);
  });

  it("does not show stale banner when listStaleSessions returns empty", async () => {
    const { listStaleSessions } = await import("./lib/wails");
    (listStaleSessions as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await tick();
    expect(screen.queryByTestId("stale-banner")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Dirty-worktree force-remove path
// ---------------------------------------------------------------------------
describe("App.svelte dirty worktree force-remove", () => {
  it("dirty worktree on remove surfaces a force-confirm that calls forceRemoveWorkspace", async () => {
    vi.useFakeTimers();
    const { listWorkspaces, removeWorkspace, forceRemoveWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    // Wails marshals Go errors to STRINGS — reject with a bare string, exactly like production.
    (removeWorkspace as ReturnType<typeof vi.fn>).mockRejectedValue("worktree has uncommitted changes");
    (forceRemoveWorkspace as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => screen.getByText("Alpha"));

    // 1. Select Alpha and trigger requestRemove via command palette → session:remove
    const alphaBtn = screen.getByRole("button", { name: "Alpha" });
    await fireEvent.click(alphaBtn);
    // Resume preview appears — confirm to open
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("option", { name: /remove session/i }));
    await tick();

    // 2. Confirm the remove ConfirmDialog (confirmLabel = "Remove")
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "confirm" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await tick();

    // 3. Advance the undo timer so the deferred removeWorkspace() runs and rejects
    vi.advanceTimersByTime(7000);
    await vi.runAllTimersAsync();

    vi.useRealTimers();

    // After the rejection, the force-confirm dialog must appear (confirmLabel "Force remove").
    await waitFor(() => expect(screen.getByRole("button", { name: /force remove/i })).toBeInTheDocument());
    expect(forceRemoveWorkspace).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole("button", { name: /force remove/i }));
    await waitFor(() => expect(forceRemoveWorkspace).toHaveBeenCalledWith("ws-1"));
  });
});

describe("App.svelte home-view persistent shell (4.4b)", () => {
  it("home view renders welcome card AND a ShellDrawer with paneId shell-home", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]); // no sessions → home view
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => expect(screen.getByText(/welcome to perch/i)).toBeInTheDocument());
    const probe = await waitFor(() => screen.getByTestId("shell-drawer-probe"));
    expect(probe.getAttribute("data-pane-id")).toBe("shell-home");
  });

  it("home shell paneId shell-home is not a session shell paneId", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    const probe = await waitFor(() => screen.getByTestId("shell-drawer-probe")); // MUST exist (teeth)
    const paneId = probe.getAttribute("data-pane-id") ?? "";
    expect(paneId).toBe("shell-home");
    expect(paneId).not.toMatch(/^shell-ws-/);
  });

  it("selecting a session switches away from home view to session view", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => screen.getByText("Alpha"));
    await fireEvent.click(screen.getByRole("button", { name: /Alpha/i }));
    // resume preview → confirm Open (4.2 flow)
    const openBtn = await waitFor(() => screen.getByRole("button", { name: /^open$/i }));
    await fireEvent.click(openBtn);
    await waitFor(() => expect(openWorkspace).toHaveBeenCalled());
    expect(screen.queryByText(/welcome to perch/i)).toBeNull();
  });
});
