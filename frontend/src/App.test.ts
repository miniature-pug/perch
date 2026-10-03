// frontend/src/App.test.ts
import { render, screen, fireEvent, waitFor, within } from "@testing-library/svelte";
import { tick } from "svelte";
import { vi, describe, it, expect, beforeEach, afterEach } from "vitest";

// Stub ShellDrawer. ShellDrawer imports xterm, and xterm crashes jsdom.
// The probe stub exposes the paneId and cwd props.
// The regression test uses these props to check that the shell drawer uses a safe pane key with no colon.
vi.mock("./lib/ShellDrawer.svelte", async () => ({
  default: (await import("./lib/__stubs__/ShellDrawerProbe.svelte")).default,
}));

// Stub heavy children. xterm and CodeMirror crash jsdom. Wails calls inside $effect would throw an error.
vi.mock("./lib/Terminal.svelte", async () => ({
  default: (await import("./lib/__stubs__/TerminalProbe.svelte")).default,
}));

// Editor stub. It renders the standard probe div and adds a "send to agent" button.
// App.test.ts uses the button to check that the onSendToAgent prop is wired and that writeToPty fires.
// The stub uses the svelte/internal/client APIs (from_html) so Svelte tracks the DOM nodes correctly for unmount.
// from_html's factory calls assign_nodes, which registers start and end nodes with the active Svelte effect.
// This lets {#if} branches tear down correctly.
let _editorSendToAgent: ((text: string) => void) | undefined;
vi.mock("./lib/Editor.svelte", async () => {
  // eslint-disable-next-line @typescript-eslint/ban-ts-comment
  // @ts-ignore: svelte/internal/client is a private module with no type declarations.
  const $ = await import("svelte/internal/client");
  // Root template: a div with a trigger button.
  // from_html returns a factory. Each call clones the template and calls assign_nodes.
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

// Captured callbacks for the wails event helpers. beforeEach resets these callbacks.
const captured = {
  agent:           [] as Array<(ev: any) => void>,
  notify:          [] as Array<(n: any)  => void>,
  fsChanged:       [] as Array<(p: any)  => void>,
  workspaceAttach: [] as Array<(p: any)  => void>,
  workspaceRelaunch: [] as Array<(p: any) => void>,
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
  createWorkspace: vi.fn(async (_agent: string, _repo: string, _baseRef: string, _branch: string, _title: string, _worktree: boolean) => ({
    id: "ws-new", title: "New", branch: "main", state: "idle",
    worktreePath: "/tmp/new", agent: "claude", paneId: "p-new", lastActive: "",
    repoPath: "/repo/New",
    caps: { approvals: false, attention: false },
  })),
  setWorkspaceTitle: vi.fn(async (_id: string, _title: string) => {}),
  workspaceForBranch: vi.fn(async (_repoPath: string, _branch: string) => ({ id: "", found: false })),
  removeWorkspace: vi.fn(async () => {}),
  writeToPty:      vi.fn(async () => {}),
  closeShell:      vi.fn(async () => {}),
  reloadAgentEnv:  vi.fn(async () => {}),
  branches:        vi.fn(async (_repo: string) => ["main", "feat/x"]),
  discoverRepos:   vi.fn(async () => [
    { path: "/discovered/repo-a", name: "repo-a", branch: "main", worktrees: [] },
  ]),
  onAgentEvent:    vi.fn((cb) => { captured.agent.push(cb);     return () => {}; }),
  onNotify:        vi.fn((cb) => { captured.notify.push(cb);    return () => {}; }),
  onFsChanged:     vi.fn((cb) => { captured.fsChanged.push(cb); return () => {}; }),
  onWorkspaceAttach: vi.fn((cb) => { captured.workspaceAttach.push(cb); return () => {}; }),
  onWorkspaceRelaunch: vi.fn((cb) => { captured.workspaceRelaunch.push(cb); return () => {}; }),
  diffStat:        vi.fn(async (worktreePath: string) => {
    // Return 2 files for /tmp/alpha, with a total of +5 and -2 changes. Return an empty list for all other paths.
    // This does not affect existing tests, because they do not check diffstat values.
    // It lets the diffstat tests check a known, non-zero total.
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
  pendingApprovals:      vi.fn(async () => []),
  clipboardSetText:      vi.fn(async () => {}),
  clipboardText:         vi.fn(async () => ""),
}));

// NOTE: the layout and mode stores are not mocked. The tests use the real $state runes stores.
// restore() calls getLayout(), which is mocked to return "{}". This makes onMount safe.
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
    repoPath: "/repo/repo-alpha",
    caps: { approvals: false, attention: false },
  },
  {
    id: "ws-2", title: "Beta", branch: "feat/beta", state: "running" as const,
    worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
    repoPath: "/repo/repo-beta",
    caps: { approvals: false, attention: false },
  },
];

beforeEach(async () => {
  vi.clearAllMocks();
  // Reset the captured callback arrays. vi.clearAllMocks does not empty them.
  captured.agent.length           = 0;
  captured.notify.length          = 0;
  captured.fsChanged.length       = 0;
  captured.workspaceAttach.length = 0;
  captured.workspaceRelaunch.length = 0;
  // Reset the real layout singleton to its default values before each test.
  const { layout } = await import("./lib/stores/layout.svelte");
  layout.setView("agent");
  layout.split = false as any;
  (layout as any).splitId   = null;
  (layout as any).sidebarW  = 240;
  (layout as any).shellH    = 200;
  (layout as any).collapsed = {};
  (layout as any).order     = [];
  // Reset the real mode singleton. The mode must be "normal" for the keymap guard to work.
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

describe("App.svelte workspace wiring", () => {
  it("renders workspaces returned by listWorkspaces in the Sidebar", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // The Sidebar renders each workspace as a button with aria-label set to ws.title.
    expect(await screen.findByRole("button", { name: /^Alpha\b/ })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /^Beta\b/ })).toBeInTheDocument();
  });

  it("selecting a workspace shows resume preview then calls openWorkspace(id) after confirm", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The preview modal appears. openWorkspace has not been called yet.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();
    // The test confirms by clicking the Open button.
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
    // The preview is gone. The workspace is now active.
    await waitFor(() =>
      expect(alphaBtn).toHaveAttribute("aria-current", "page")
    );
    expect(screen.getByRole("button", { name: /^Beta\b/ })).not.toHaveAttribute("aria-current");
  });

  it("clicking a sidebar row shows a resume preview before opening workspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => screen.getByText("Alpha"));
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
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
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));
    await waitFor(() => expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();
  });

  it("resume preview shows 'Continues the previous conversation' when willResume is true", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      { ...fakeWorkspaces[0], willResume: true },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await fireEvent.click(await screen.findByRole("button", { name: /^Alpha\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(screen.getByText("Continues the previous conversation")).toBeInTheDocument();
    expect(screen.queryByText("Starts fresh")).not.toBeInTheDocument();
  });

  it("resume preview shows 'Starts fresh' when willResume is false", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      { ...fakeWorkspaces[0], willResume: false },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await fireEvent.click(await screen.findByRole("button", { name: /^Alpha\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(screen.getByText("Starts fresh")).toBeInTheDocument();
    expect(screen.queryByText("Continues the previous conversation")).not.toBeInTheDocument();
  });

  it("resume preview shows the fork point when baseRef is present", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      { ...fakeWorkspaces[0], baseRef: "develop" },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await fireEvent.click(await screen.findByRole("button", { name: /^Alpha\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(screen.getByText("Forked from")).toBeInTheDocument();
    expect(screen.getByText("develop")).toBeInTheDocument();
  });

  it("resume preview hides the fork point entirely when baseRef is empty", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      { ...fakeWorkspaces[0], baseRef: "" },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await fireEvent.click(await screen.findByRole("button", { name: /^Alpha\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(screen.queryByText("Forked from")).not.toBeInTheDocument();
  });

  it("clicking the ALREADY-ACTIVE session is a no-op: no second openWorkspace, no resume-preview", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    // Open Alpha through the resume-preview flow.
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
    await waitFor(() => expect(alphaBtn).toHaveAttribute("aria-current", "page"));
    expect((openWorkspace as ReturnType<typeof vi.fn>).mock.calls.length).toBe(1);

    // Click the same, already active, row again. It must not reopen the session and must not show a preview.
    await fireEvent.click(alphaBtn);
    await tick();
    expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument();
    expect((openWorkspace as ReturnType<typeof vi.fn>).mock.calls.length).toBe(1);
  });

  it("clicking an OPEN-but-not-active session just FOCUSES it (no reopen, no preview)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    const betaBtn  = screen.getByRole("button", { name: /^Beta\b/ });

    // Open Alpha through the resume preview.
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));

    // Open Beta through the resume preview.
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-2"));
    await waitFor(() => expect(betaBtn).toHaveAttribute("aria-current", "page"));

    const callsBefore = (openWorkspace as ReturnType<typeof vi.fn>).mock.calls.length;

    // Click Alpha, which is open but not active. This should only refocus it, with no preview and no reopen.
    await fireEvent.click(alphaBtn);
    await tick();
    expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument();
    expect((openWorkspace as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callsBefore);
    await waitFor(() => expect(alphaBtn).toHaveAttribute("aria-current", "page"));
  });
});

describe("App.svelte Stage content routing", () => {
  it("view='agent' → TerminalProbe mounted with paneId and cwd from active workspace", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for the workspaces to load. This is post-mount, so restore() has already run.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    // Make sure the view is "agent". The real store's restore() sets the default view to "agent".
    layout.setView("agent");
    await tick();
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
    // Wait for the workspaces to load post-mount, then switch the view reactively.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
    // No path is selected at first.
    expect(editor.dataset.path).toBe("");
    // Clicking the FileTreeProbe's open button triggers onOpen, which sets codePath.
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();

    // Open a .md file with the FileTreeProbe "open markdown" button.
    const openMdBtn = screen.getByRole("button", { name: "open markdown" });
    await fireEvent.click(openMdBtn);
    await waitFor(() => {
      expect(screen.getByTestId("preview")).toBeInTheDocument();
      expect(screen.getByTestId("preview").dataset.path).toBe("/some/file.md");
      expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
    });

    // Open a .ts file with the FileTreeProbe "open file" button. The Editor mounts and the Preview is gone.
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
    // Wait for the workspaces to load post-mount, then switch the view reactively.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();

    // Open a .md file. readFile rejects this call.
    const openMdBtn = screen.getByRole("button", { name: "open markdown" });
    await fireEvent.click(openMdBtn);

    // The app must not crash. The Preview probe must still mount with the .md path.
    await waitFor(() => {
      expect(screen.getByTestId("preview")).toBeInTheDocument();
      expect(screen.getByTestId("preview").dataset.path).toBe("/some/file.md");
    });
    // The Editor must not be present. This confirms the Preview routing is correct.
    expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
  });

  it("activeId null → no child probes, empty-state placeholder shown", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // Do not select any workspace. activeId stays null.
    await screen.findByRole("button", { name: /^Alpha\b/ }); // The workspaces are loaded.
    expect(document.querySelector(".empty-state")).toBeInTheDocument();
    expect(screen.queryByTestId("terminal")).not.toBeInTheDocument();
    expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
    expect(screen.queryByTestId("diff")).not.toBeInTheDocument();
  });

  it("view switch keeps the agent terminal mounted (hidden), never destroyed, so its buffer survives", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));

    // In agent view, the terminal is visible.
    layout.setView("agent");
    await tick();
    const terminal = await screen.findByTestId("terminal");
    expect(terminal).toBeVisible();

    // Switch to code view. The editor and file tree become visible.
    // The same terminal element stays in the DOM, hidden but not unmounted, so its buffer is kept.
    layout.setView("code");
    await tick();
    await waitFor(() => {
      expect(screen.getByTestId("editor")).toBeVisible();
      expect(screen.getByTestId("filetree")).toBeVisible();
    });
    expect(screen.getByTestId("terminal")).toBe(terminal);
    expect(screen.getByTestId("terminal")).not.toBeVisible();

    // Switch back to agent view. The same terminal element is visible again.
    layout.setView("agent");
    await tick();
    await waitFor(() => expect(screen.getByTestId("terminal")).toBeVisible());
    expect(screen.getByTestId("terminal")).toBe(terminal);
  });

  it("split mode: view='agent' + split=true + splitId set → two independent TerminalProbes with distinct paneIds", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for the workspaces to load. restore() has run by now.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    // Set the agent view, turn on split, and assign splitId to the second workspace (ws-2).
    layout.setView("agent");
    layout.toggleSplit(); // Split goes from false to true.
    layout.setSplitId("ws-2");
    await tick();
    // Both the primary and secondary slots must have a TerminalProbe.
    await waitFor(() => {
      const terminals = screen.getAllByTestId("terminal");
      expect(terminals).toHaveLength(2);
    });
    // The primary pane shows Alpha with paneId p1. The secondary pane shows Beta with paneId p2.
    const primaryPane   = document.querySelector("[data-pane='primary']") as HTMLElement;
    const secondaryPane = document.querySelector("[data-pane='secondary']") as HTMLElement;
    expect(within(primaryPane).getByTestId("terminal").dataset.paneId).toBe("p1");
    expect(within(secondaryPane).getByTestId("terminal").dataset.paneId).toBe("p2");
  });

  // F10a: the split session's agent Terminal must mount exactly once.
  // Its node relocates into the secondary pane.
  // Toggling split off, then on, then off again must not destroy and recreate its xterm.
  // That would blank the frontend scrollback, even though the backend pty survives.
  // A mount creates a fresh, blank buffer, so the per-paneId mount count staying flat across the toggle is the proof.
  it("split toggle keeps the split session's Terminal single-mounted (node relocated, xterm buffer preserved) (F10a)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { terminalMountCounts } = await import("./lib/__stubs__/terminalExit");
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));

    // Baseline: the mount registry is a module singleton shared across tests.
    // Measure the delta this test causes instead of an absolute count.
    const p2Node = () => within(document.body).getAllByTestId("terminal").find(n => n.dataset.paneId === "p2") ?? null;
    const secondaryPane = () => document.querySelector("[data-pane='secondary']") as HTMLElement | null;
    const baseline = terminalMountCounts["p2"] ?? 0;

    // Turn split on. The secondary pane is ws-2, with paneId p2.
    layout.setView("agent");
    layout.setSplit(true);
    layout.setSplitId("ws-2");
    await tick();

    // The split terminal (p2) relocates into the secondary pane.
    // It has mounted exactly once more than the baseline.
    await waitFor(() => {
      expect(secondaryPane()).toBeTruthy();
      expect(within(secondaryPane()!).getByTestId("terminal").dataset.paneId).toBe("p2");
    });
    const splitNode = within(secondaryPane()!).getByTestId("terminal");
    expect(terminalMountCounts["p2"] ?? 0).toBe(baseline + 1);

    // Toggle split off. The split node must not be destroyed.
    // It returns to the hidden primary keep-alive loop. The DOM node stays the same, and the mount count does not change.
    layout.setSplit(false);
    await tick();
    await waitFor(() => expect(secondaryPane()).toBeFalsy());
    expect(p2Node()).toBe(splitNode); // This is the same live node, not a rebuilt, blank one.
    expect(terminalMountCounts["p2"] ?? 0).toBe(baseline + 1);

    // Toggle split on again. The same node is re-adopted into the secondary pane, with no remount.
    // A remount would produce a blank xterm. Avoiding that remount is the whole point of the fix.
    layout.setSplit(true);
    await tick();
    await waitFor(() => {
      expect(secondaryPane()).toBeTruthy();
      expect(within(secondaryPane()!).getByTestId("terminal").dataset.paneId).toBe("p2");
    });
    expect(within(secondaryPane()!).getByTestId("terminal")).toBe(splitNode);
    expect(terminalMountCounts["p2"] ?? 0).toBe(baseline + 1);
  });

  // A backend relaunch keeps the conversation. perch reload, or the drawer's env-to-agent button, triggers it.
  // The relaunch respawns the agent pty under the same paneId.
  // The app must remount that session's agent terminal, so the new `claude --resume` draws into a fresh xterm.
  // Without the remount, `claude --resume` draws over the stale buffer, which garbles the display after reload.
  // The workspace:relaunch event signals this. It bumps the session's terminal epoch.
  // The epoch bump re-keys the {#each} block and remounts the TerminalProbe exactly once.
  it("workspace:relaunch remounts ONLY that workspace's agent terminal (fresh xterm)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { terminalMountCounts } = await import("./lib/__stubs__/terminalExit");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open ws-1, with paneId p1, so its agent terminal mounts.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() =>
      expect(within(document.body).getAllByTestId("terminal").some(n => n.dataset.paneId === "p1")).toBe(true),
    );

    // The app must have registered a relaunch listener on mount.
    expect(captured.workspaceRelaunch.length).toBeGreaterThan(0);
    const baseline = terminalMountCounts["p1"] ?? 0;

    // Relaunch ws-1. Its agent terminal remounts exactly once, with a fresh xterm.
    captured.workspaceRelaunch.forEach(cb => cb({ workspaceId: "ws-1" }));
    await tick();
    await waitFor(() => expect(terminalMountCounts["p1"] ?? 0).toBe(baseline + 1));

    // A relaunch for a DIFFERENT workspace must NOT remount ws-1's terminal.
    const afterOwn = terminalMountCounts["p1"] ?? 0;
    captured.workspaceRelaunch.forEach(cb => cb({ workspaceId: "ws-2" }));
    await tick();
    expect(terminalMountCounts["p1"] ?? 0).toBe(afterOwn);
  });

  // The shell drawer pane key must have a safe shape.
  // The old key was "{wsid}:shell". Go's validateSessionID charset [A-Za-z0-9_-] rejects the colon.
  // OpenShell rejected that id, so the drawer never connected to a pty.
  // The key is now "shell-{wsid}".
  // Check that the rendered paneId starts with "shell-" and has no colon or other out-of-charset character.
  it("shell drawer paneId is the safe 'shell-{wsid}' shape (no colon)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // ShellDrawer renders under {#if active}. Select a workspace first.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // After the home-shell-mount fix, the DOM may have two shell-drawer-probes: shell-home hidden and shell-ws-1 visible.
    // Filter to the session shell explicitly.
    const probes = await screen.findAllByTestId("shell-drawer-probe");
    const drawer = probes.find(p => p.getAttribute("data-pane-id") === "shell-ws-1");
    expect(drawer).toBeDefined();
    const paneId = drawer!.dataset.paneId!;
    expect(paneId).toBe("shell-ws-1");
    expect(paneId.startsWith("shell-")).toBe(true);
    // This mirrors Go's validateSessionID charset. It has no colon and no other invalid characters.
    expect(paneId).toMatch(/^[A-Za-z0-9_-]+$/);
  });
});

describe("App.svelte MenuBar + CommandPalette", () => {
  it("pressing ':' in NORMAL opens the CommandPalette (dialog appears)", async () => {
    const { default: App } = await import("./App.svelte");
    render(App);
    // The palette must not be visible at first.
    expect(screen.queryByRole("dialog", { name: "command palette" })).not.toBeInTheDocument();
    // Fire a ':' keydown. onKeyDown calls mode.enterCommand(), which sets mode.current to "command".
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
    // The command ran. layout.view changed.
    expect(layout.view).toBe("code");
    // The palette closed. The mode is back to normal.
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

describe("App.svelte live event wiring", () => {
  it("onAgentEvent: state flip updates Sidebar status label for that workspace", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for the workspaces to load.
    await screen.findByRole("button", { name: /^Alpha\b/ });

    // Alpha starts as "idle". The Sidebar shows "idle".
    expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("idle");

    // Fire an agent event. It flips Alpha to "awaiting-approval" and carries an approval payload.
    const cb = captured.agent.at(-1)!;
    cb({
      workspaceId: "ws-1",
      kind: "approval",
      state: "awaiting-approval",
      approval: { reqId: "req-1", tool: "bash", summary: "Run script" },
    });
    await tick();

    // The Sidebar maps "awaiting-approval" to "needs you".
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("needs you")
    );
  });

  it("onAgentEvent: a 'question' event flips state to awaiting-input and shows NO approval card (signal-only)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha. Any approval dock is then scoped to the active session.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Fire a question event on ws-2, a background session.
    // Its left-pane badge is meaningful, because the active and viewed session's badge is acknowledged away.
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-2", kind: "question", state: "awaiting-input" });
    await tick();

    // The Sidebar maps "awaiting-input" to "asking you" for the background session.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("asking you")
    );

    // A question is signal-only. It shows no approval dock and no Allow button.
    expect(document.querySelector("[data-zone='approval-dock']")).toBeNull();
    expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument();
  });

  // -------------------------------------------------------------------------
  // FIX C3: the left-pane "asking you a question" (awaiting-input) badge is a background signal.
  // It must clear once the user is actively viewing that session.
  // It must not reappear for the same question.
  // A new question must raise it again.
  // The awaiting-approval signal must never be suppressed this way.
  // -------------------------------------------------------------------------

  it("awaiting-input on a BACKGROUND session shows the 'asking you' signal", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    // No session is selected. ws-2 is a background session.
    await screen.findByRole("button", { name: /^Beta\b/ });

    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-2", kind: "question", state: "awaiting-input" });
    await tick();

    // The background session's badge is shown.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("asking you")
    );
  });

  it("awaiting-input signal CLEARS once the session becomes active + agent pane is viewed", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Raise the question on ws-2 while it is a background session, in agent view.
    layout.setView("agent");
    await screen.findByRole("button", { name: /^Beta\b/ });
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-2", kind: "question", state: "awaiting-input" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("asking you")
    );

    // Model the real backend. Once the agent is blocked on a question, the monitor's CurrentState() reports "awaiting-input".
    // So the ListWorkspaces() refetch that openSession runs after openWorkspace() returns ws-2 in that state.
    // Opening a session does not answer its question. The user answers it in the agent's own TUI.
    // Without this mock, the shared fakeWorkspaces stub would report the stale "running" state.
    // That would clobber the awaiting-input signal that the refetch is meant to preserve.
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(
      fakeWorkspaces.map(w => (w.id === "ws-2" ? { ...w, state: "awaiting-input" as const } : w)),
    );

    // Now the user opens and focuses ws-2, with the agent pane visible and active.
    // The signal has done its job and must clear.
    // The underlying ws.state stays "awaiting-input", but the Sidebar suppresses the badge.
    // The row reads as a neutral "idle".
    const betaBtn = screen.getByRole("button", { name: /^Beta\b/ });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    await waitFor(() => {
      const row = screen.getByRole("button", { name: /^Beta\b/ });
      expect(row).not.toHaveTextContent("asking you");
      expect(row).toHaveTextContent("idle");
    });
  });

  it("two consecutive question events in the SAME turn (no state change) both re-raise on a background session", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // ws-2 is a background session, with no session selected. Use the code view, so it is never "viewed".
    // The badge stays meaningful throughout.
    layout.setView("code");
    await screen.findByRole("button", { name: /^Beta\b/ });
    const cb = captured.agent.at(-1)!;

    // First question raises the badge on the backgrounded session.
    cb({ workspaceId: "ws-2", kind: "question", state: "awaiting-input" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("asking you")
    );

    // Simulate the user acknowledging the question, as if it had been viewed.
    // Drop the pending signal by opening and viewing it briefly, then background it again.
    // The simpler way here: view the agent pane to acknowledge it, then return to code view.
    layout.setView("agent");
    const betaBtn = screen.getByRole("button", { name: /^Beta\b/ });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    // The question is acknowledged, active, and in agent view, so the badge is suppressed.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).not.toHaveTextContent("asking you")
    );
    // Background it again in code view. It is still acknowledged, so there is no badge.
    layout.setView("code");
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).not.toHaveTextContent("asking you")
    );

    // The agent asks a second question in the same turn, with no intervening Stop or running state.
    // So ws.state is already "awaiting-input".
    // The un-acknowledge check now keys on the question event (kind === "question"), not the state-value edge.
    // So the check fires, and the badge re-raises on the backgrounded session, even though prev === "awaiting-input".
    cb({ workspaceId: "ws-2", kind: "question", state: "awaiting-input" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("asking you")
    );
  });

  // -------------------------------------------------------------------------
  // USER BUG REPRO: "If I switch to a different session, all non-highlighted
  // sessions in the left pane stop giving their status updates."
  //
  // The background test above fires an event on a session that was never made active.
  // This block reproduces the reported flow exactly.
  // It opens two sessions, so the user has actively switched between them.
  // Then it fires a stream of agent:event state frames at the now-background session.
  // It checks that the session's sidebar row reflects every new state live.
  // This exercises both switch paths in App.svelte onSelect (App.svelte:710):
  // the openSession() path (App.svelte:753, which runs `workspaces = await listWorkspaces()` and replaces the reactive array),
  // and the pure focus path `if (openIds.has(id)) { activeId = id; return; }` (App.svelte:725, no refetch).
  // -------------------------------------------------------------------------
  it("USER REPRO: after switching active session, the now-BACKGROUND session keeps updating its status live (both switch paths)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha (ws-1). It becomes the active, open session.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Switch to Beta (ws-2). Opening it makes Beta active and pushes Alpha into the background.
    // openSession() ran `workspaces = await listWorkspaces()`, so the reactive array was replaced right before the background frames fire.
    const betaBtn = screen.getByRole("button", { name: /^Beta\b/ });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const cb = captured.agent.at(-1)!;

    // --- Background session A (Alpha, ws-1) must update live across frames. ---
    cb({ workspaceId: "ws-1", kind: "state", state: "running" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("running")
    );

    cb({ workspaceId: "ws-1", kind: "state", state: "done" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("done")
    );

    // A second idle frame arrives after done. This is a shape opencode can emit, and it is still applied.
    cb({ workspaceId: "ws-1", kind: "state", state: "idle" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("idle")
    );

    // --- Now switch back to Alpha through the pure focus path, with no refetch.
    // This backgrounds Beta (ws-2). Alpha is already open, so onSelect takes the
    // `activeId = id; return;` branch (App.svelte:725). Beta must then keep updating live too. ---
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
    await tick();

    cb({ workspaceId: "ws-2", kind: "state", state: "done" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("done")
    );

    cb({ workspaceId: "ws-2", kind: "state", state: "running" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("running")
    );
  });

  // -------------------------------------------------------------------------
  // USER BUG REPRO, array-replacement variant. This is step 3 of the diagnostic.
  // A switch to a not-yet-open session triggers `workspaces = await listWorkspaces()`.
  // The backend returns brand-new object instances. This is the real Wails case, not the shared-reference fixture.
  // The previous reactive proxy elements become orphaned.
  // Check that a later background agent:event still finds the session in the fresh array and mutates it reactively.
  // -------------------------------------------------------------------------
  it("USER REPRO: background reactivity survives a listWorkspaces() refetch that returns FRESH objects", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    // Every call returns a brand-new array of brand-new objects.
    // This is a genuine array replacement, unlike mockResolvedValue(fakeWorkspaces)'s shared reference.
    (listWorkspaces as ReturnType<typeof vi.fn>).mockImplementation(async () =>
      fakeWorkspaces.map(w => ({ ...w, caps: { ...w.caps } })),
    );
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha, then switch to Beta. Each openSession call replaces the array.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const betaBtn = screen.getByRole("button", { name: /^Beta\b/ });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Background Alpha (ws-1) must still update after the array was replaced with
    // fresh objects.
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "state", state: "done" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("done")
    );
  });

  // -------------------------------------------------------------------------
  // USER BUG REPRO: "the begging never goes away even after I switch the
  // tab/session." A finished session, done or errored, has row-level begging.
  // The begging must stop once the user opens the session.
  // It must stay stopped when the user switches away.
  // The persistent ✓/✗ status stays visible.
  // A fresh finish begs again.
  // -------------------------------------------------------------------------
  it("USER REPRO: a done session you already opened stops begging after you switch away; a FRESH done re-begs", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha (ws-1), then open Beta (ws-2). Beta becomes active, and Alpha becomes background and open.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    const betaBtn = screen.getByRole("button", { name: /^Beta\b/ });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const cb = captured.agent.at(-1)!;

    // Alpha finishes while in the background. Its row begs, with the attn-done class.
    cb({ workspaceId: "ws-1", kind: "state", state: "done" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ }).classList.contains("attn-done")).toBe(true)
    );

    // The user opens Alpha, which is already open, so this takes the pure focus path and sets activeId to ws-1.
    // While active, Alpha never begs. Being viewed records it as seen (attnDoneAck).
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
    await tick();
    expect(screen.getByRole("button", { name: /^Alpha\b/ }).classList.contains("attn-done")).toBe(false);

    // Switch away to Beta. Alpha is background and still done, but seen, so it does not beg again.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await tick();
    expect(screen.getByRole("button", { name: /^Alpha\b/ }).classList.contains("attn-done")).toBe(false);
    // Alpha's persistent status stays. The row still reads "done".
    expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("done");

    // A fresh turn on Alpha, from running to done, while in the background, raises the beg again.
    cb({ workspaceId: "ws-1", kind: "state", state: "running" });
    await tick();
    cb({ workspaceId: "ws-1", kind: "state", state: "done" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ }).classList.contains("attn-done")).toBe(true)
    );
  });

  it("awaiting-APPROVAL is never suppressed by the ack path (stays 'needs you' when active+viewed)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    layout.setView("agent");
    const betaBtn = await screen.findByRole("button", { name: /^Beta\b/ });
    // Active + viewed (this would ack an awaiting-input, but must NOT touch approval).
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const cb = captured.agent.at(-1)!;
    cb({
      workspaceId: "ws-2",
      kind: "approval",
      state: "awaiting-approval",
      approval: { reqId: "req-appr", tool: "bash", summary: "Run script" },
    });
    await tick();

    // The approval attention persists on the active, viewed session.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveTextContent("needs you")
    );
  });

  it("onNotify: blocking tier calls addBlocking and appears in notifications store", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });

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

  // FIX F: a blocking "Question" notif is lit while the agent is blocked, but when
  // the user answers in the agent's own TUI perch auto-allows (no decideOne), the
  // agent resumes to "running", and the notif would otherwise stay lit forever.
  // A state transition into a resolved state must clear the session's blocking notifs.
  it("blocking notif clears when the session transitions from awaiting-input to running", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Beta\b/ });

    const { getItems } = await import("./lib/stores/notifications.svelte");
    const agentCb = captured.agent.at(-1)!;

    // The agent blocks on a question (ws-2 starts "running" in the fixture, so
    // move it into a blocking state first for a genuine resolve edge below).
    agentCb({ workspaceId: "ws-2", kind: "question", state: "awaiting-input" });
    await tick();

    // The blocking "Question" notification lights up for ws-2.
    const notifyCb = captured.notify.at(-1)!;
    notifyCb({ tier: "blocking", title: "Question", body: "Which option?", workspaceId: "ws-2" });
    await tick();
    expect(getItems().some(n => n.workspaceId === "ws-2" && n.tier === "blocking")).toBe(true);

    // The user answers in the agent TUI. perch auto-allows, and the agent resumes.
    agentCb({ workspaceId: "ws-2", kind: "state", state: "running" });
    await tick();

    // The stale blocking notif for ws-2 must be gone.
    expect(getItems().some(n => n.workspaceId === "ws-2" && n.tier === "blocking")).toBe(false);
  });

  // FIX F: a transition into "errored" is not a resolution.
  // An unresolved error must keep its blocking notification.
  it("blocking notif is NOT cleared when the session transitions to errored", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Beta\b/ });

    const { getItems } = await import("./lib/stores/notifications.svelte");
    const notifyCb = captured.notify.at(-1)!;
    notifyCb({ tier: "blocking", title: "Agent error", body: "boom", workspaceId: "ws-2" });
    await tick();
    expect(getItems().some(n => n.workspaceId === "ws-2" && n.tier === "blocking")).toBe(true);

    const agentCb = captured.agent.at(-1)!;
    agentCb({ workspaceId: "ws-2", kind: "state", state: "errored" });
    await tick();

    // Still lit. An unresolved error keeps its notification.
    expect(getItems().some(n => n.workspaceId === "ws-2" && n.tier === "blocking")).toBe(true);
  });

  // STALE-QUESTION FIX: the agent asks a question. This moves the state to awaiting-input and lights a blocking "Question" notification.
  // Its next tool then needs approval. This moves the state from awaiting-input to awaiting-approval.
  // The resolved-state clear only fires on running, idle, or done.
  // So this edge left the superseded "Question" notification lingering unread beside the fresh "Approval needed" notification.
  // Leaving awaiting-input must drop only that question notification.
  // It must not touch a pending "Approval needed" notification. claude can have several of these queued at once.
  // This test fails on a revert: without the drop, the Question notification survives.
  it("leaving awaiting-input clears the stale Question notif but keeps every pending Approval", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Beta\b/ });

    const { getItems, dropForWorkspace } = await import("./lib/stores/notifications.svelte");
    // Isolate ws-2 (the store is a module singleton shared across tests).
    dropForWorkspace("ws-2");

    const agentCb  = captured.agent.at(-1)!;
    const notifyCb = captured.notify.at(-1)!;

    // The agent asks a question. The state moves to awaiting-input, which lights a blocking "Question" notification.
    agentCb({ workspaceId: "ws-2", kind: "question", state: "awaiting-input" });
    await tick();
    notifyCb({ tier: "blocking", title: "Question", body: "Which option?", workspaceId: "ws-2", state: "awaiting-input" });
    // TWO approvals queue for the same session (claude approve-all semantics), each
    // arriving as its own blocking "Approval needed" notif tagged awaiting-approval.
    notifyCb({ tier: "blocking", title: "Approval needed", body: "run bash",  workspaceId: "ws-2", state: "awaiting-approval" });
    notifyCb({ tier: "blocking", title: "Approval needed", body: "write file", workspaceId: "ws-2", state: "awaiting-approval" });
    await tick();

    // Precondition: the Question and both approvals coexist for ws-2.
    expect(getItems().filter(n => n.workspaceId === "ws-2" && n.title === "Question")).toHaveLength(1);
    expect(getItems().filter(n => n.workspaceId === "ws-2" && n.title === "Approval needed")).toHaveLength(2);

    // The agent leaves awaiting-input straight into awaiting-approval for its next tool.
    // The resolved-state clear, which covers running, idle, or done, never covers this edge.
    agentCb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval" });
    await tick();

    // The superseded Question notification is gone.
    expect(getItems().some(n => n.workspaceId === "ws-2" && n.title === "Question")).toBe(false);
    // Both pending approvals survive untouched. This is the hard multi-approval invariant.
    expect(getItems().filter(n => n.workspaceId === "ws-2" && n.title === "Approval needed")).toHaveLength(2);
  });

  it("onNotify: ambient tier routes to addAmbient in the store", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });

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

  // The user asked for notifications to "auto-read" when they switch to the session the event was about.
  // Switching to a session, meaning an activeId change with the window focused, is the catch-up.
  // Its hub notifications go read non-destructively. They stay in the hub history and only clear from the unread bell badge.
  it("switching to a session auto-reads its hub notifications (bell badge clears on view)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Beta\b/ });

    const { getItems, dropForWorkspace } = await import("./lib/stores/notifications.svelte");
    // The store is a shared singleton across tests. Start these ids clean.
    dropForWorkspace("ws-1");
    dropForWorkspace("ws-2");
    // Auto-read is gated on the window having focus.
    window.dispatchEvent(new Event("focus"));
    await tick();

    // A background session, ws-2, not active, finishes a turn. Its notification is unread.
    const notifyCb = captured.notify.at(-1)!;
    notifyCb({ tier: "ambient", title: "Turn complete", body: "Agent finished a turn.", workspaceId: "ws-2" });
    await tick();
    expect(getItems().filter((n) => n.workspaceId === "ws-2" && !n.read).length).toBe(1);

    // Switch to ws-2. It is cold, so confirm the resume preview. Viewing it auto-reads the notification.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    await waitFor(() =>
      expect(getItems().filter((n) => n.workspaceId === "ws-2" && !n.read).length).toBe(0)
    );
    // Non-destructive: the entry stays in the hub history (read, not dropped).
    expect(getItems().some((n) => n.workspaceId === "ws-2" && n.title === "Turn complete")).toBe(true);
  });

  // A live event on the session the user is already watching must still bump the bell.
  // Otherwise the completion signal is silently swallowed. This is the opencode "finished a turn but no notification" regression.
  // Auto-read is only a catch-up on an active or focus change, as in the test above.
  // It never fires for a fresh event on the current session.
  it("a turn-done notification for the session already on screen still bumps the bell (not swallowed on arrival)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Beta\b/ });

    const { getItems, dropForWorkspace } = await import("./lib/stores/notifications.svelte");
    dropForWorkspace("ws-2");
    window.dispatchEvent(new Event("focus"));
    await tick();

    // Make ws-2 the active, on-screen session.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // A turn completes on the session the user is watching. The notification stays unread, so the bell bumps.
    // The user still wants to know the turn finished.
    const notifyCb = captured.notify.at(-1)!;
    notifyCb({ tier: "ambient", title: "Turn complete", body: "Agent finished a turn.", workspaceId: "ws-2" });
    await tick();

    expect(getItems().some((n) => n.workspaceId === "ws-2" && n.title === "Turn complete")).toBe(true);
    expect(getItems().filter((n) => n.workspaceId === "ws-2" && !n.read).length).toBe(1);
  });

  it("onFsChanged: refreshes DiffView IN PLACE (node identity preserved, not remounted) (F4)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha and switch to diff view
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("diff");
    await tick();

    const diffBefore = await screen.findByTestId("diff");
    expect(diffBefore).toBeInTheDocument();

    // Fire fs:changed for ws-1. The DiffView must refresh through its `refresh` prop, not remount through a {#key}.
    // Remounting would collapse expanded hunks and reset scroll on every agent file write.
    const cb = captured.fsChanged.at(-1)!;
    cb({ workspaceId: "ws-1", path: "/tmp/alpha/some-file.ts" });
    await tick();
    await tick();

    // This is the same DOM node. The view refreshed in place and was never torn down.
    expect(screen.getByTestId("diff")).toBe(diffBefore);
  });

  it("view switch keeps the DiffView mounted (hidden), never destroyed (F3)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));

    // In diff view, the DiffView is visible.
    layout.setView("diff");
    await tick();
    const diff = await screen.findByTestId("diff");
    expect(diff).toBeVisible();

    // Switch to the agent view. The same DiffView node stays in the DOM, hidden.
    // Its expanded hunks and scroll survive. This mirrors the terminal keep-alive.
    layout.setView("agent");
    await tick();
    expect(screen.getByTestId("diff")).toBe(diff);
    expect(screen.getByTestId("diff")).not.toBeVisible();

    // Back to diff view. It is still the same node, visible again.
    layout.setView("diff");
    await tick();
    await waitFor(() => expect(screen.getByTestId("diff")).toBeVisible());
    expect(screen.getByTestId("diff")).toBe(diff);
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

describe("App.svelte approval card + notification hub", () => {
  // Workspace with caps.approvals=true so ApprovalCard actually renders.
  const approvalWorkspaces = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: true, attention: false },
    },
  ];

  it("ApprovalCard renders in docked chrome when active workspace has a pending approval", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(approvalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select the workspace to make it active
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

    // The card must be visible. Check for the summary text and the Allow button.
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

    // Click Allow. approve() will reject.
    await fireEvent.click(screen.getByRole("button", { name: "Allow" }));

    // (a) The ApprovalCard must still be present. It is not dequeued.
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

    await screen.findByRole("button", { name: /^Alpha\b/ });

    // Inject a notification via the notify callback
    const cb = captured.notify.at(-1)!;
    cb({ tier: "blocking", title: "Hub test notification", body: "Hub body", workspaceId: "ws-1" });
    await tick();

    // NotificationHub now renders behind {#if notifOpen}. Open it first with the bell.
    const bellBtn = screen.getByRole("menuitem", { name: "notifications" });
    await fireEvent.click(bellBtn);
    await tick();

    // Hub must now be visible
    expect(screen.getByRole("region", { name: "notification hub" })).toBeInTheDocument();

    // The notification title should appear in the hub
    await waitFor(() =>
      expect(screen.getByText("Hub test notification")).toBeInTheDocument()
    );
  });

  it("opening the hub marks all items read → unread count drops to 0 (badge clears)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(approvalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });

    const { getItems } = await import("./lib/stores/notifications.svelte");

    // Inject a blocking notification, which never auto-dismisses. It starts unread.
    const cb = captured.notify.at(-1)!;
    cb({ tier: "blocking", title: "Unread thing", body: "b", workspaceId: "ws-1" });
    await tick();
    expect(getItems().filter((n) => !n.read).length).toBeGreaterThan(0);

    // Open the hub with the bell. Opening the hub is the catch-up, so all items go read.
    const bellBtn = screen.getByRole("menuitem", { name: "notifications" });
    await fireEvent.click(bellBtn);
    await tick();

    // Unread count is now 0; items remain in the hub (still shown, just read).
    await waitFor(() => expect(getItems().filter((n) => !n.read).length).toBe(0));
    expect(getItems().some((n) => n.title === "Unread thing")).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// Multi-terminal shell drawer (tabs / +, × / split)
// ShellDrawer is mocked to a probe. ShellPanel, which hosts the tabs, +, ×, and split controls, is real.
// So these tests check the App, ShellPanel, and shellPanes wiring end to end.
// The probe surfaces its paneId as data-pane-id.
// ---------------------------------------------------------------------------
describe("App.svelte multi-terminal shell drawer", () => {
  async function openAlpha(): Promise<HTMLElement> {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    return document.querySelector('[data-zone="shell-drawer"]') as HTMLElement;
  }
  const paneIds = (zone: HTMLElement) =>
    Array.from(zone.querySelectorAll('[data-testid="shell-drawer-probe"]'))
      .map((p) => p.getAttribute("data-pane-id"));
  const shownCells = (zone: HTMLElement) =>
    Array.from(zone.querySelectorAll(".shell-cell"))
      .filter((c) => (c as HTMLElement).style.display !== "none");

  it("opens a session with exactly one default shell tab and cell", async () => {
    const zone = await openAlpha();
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(1));
    expect(paneIds(zone)).toEqual(["shell-ws-1"]);
    expect(shownCells(zone)).toHaveLength(1);
  });

  it("+ adds a terminal with a fresh backend-recoverable id; a tab click switches the shown cell", async () => {
    const zone = await openAlpha();
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(1));

    await fireEvent.click(within(zone).getByRole("button", { name: "new terminal" }));
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(2));
    expect(paneIds(zone)).toEqual(["shell-ws-1", "shell-ws-1_1"]);
    expect(shownCells(zone)).toHaveLength(1); // tabs mode: one shown at a time

    // Switch back to the first tab. The shown cell is now the default shell.
    await fireEvent.click(within(zone).getAllByRole("tab")[0]);
    await tick();
    const shown = shownCells(zone)[0].querySelector('[data-testid="shell-drawer-probe"]');
    expect(shown?.getAttribute("data-pane-id")).toBe("shell-ws-1");
  });

  it("× on the last remaining tab reaps it AND spawns a fresh replacement (never empty)", async () => {
    const { closeShell } = await import("./lib/wails");
    const zone = await openAlpha();
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(1));

    await fireEvent.click(within(zone).getByRole("button", { name: /^close / }));
    await tick();

    expect(closeShell).toHaveBeenCalledWith("shell-ws-1"); // the closed pty is reaped
    // The drawer is never empty. One tab remains, with a new, unreused id.
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(1));
    expect(paneIds(zone)).toEqual(["shell-ws-1_1"]);
  });

  it("split shows two cells side by side; closing back below two clears the split", async () => {
    const zone = await openAlpha();
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(1));

    // Splitting with a single shell mints a partner. This gives two tabs and two shown cells.
    await fireEvent.click(within(zone).getByRole("button", { name: "split terminals side by side" }));
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(2));
    await waitFor(() => expect(shownCells(zone)).toHaveLength(2));

    // Close one. Only one shell remains, the split clears, and one cell shows.
    await fireEvent.click(within(zone).getAllByRole("button", { name: /^close / })[1]);
    await waitFor(() => expect(within(zone).getAllByRole("tab")).toHaveLength(1));
    expect(shownCells(zone)).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// Dialogs + DragDrop
// ---------------------------------------------------------------------------

describe("App.svelte NewSessionDialog", () => {
  it("Sidebar onNew / openNewSession opens the dialog; submitting calls createWorkspace and refreshes", async () => {
    const { listWorkspaces, createWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        {
          id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
          worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
          repoPath: "/repo/repo-alpha",
          caps: { approvals: false, attention: false },
        },
      ])
      .mockResolvedValue([]); // subsequent listWorkspaces after create

    const { default: App } = await import("./App.svelte");
    render(App);

    // Workspaces loaded
    await screen.findByRole("button", { name: /^Alpha\b/ });

    // Dialog must not be visible yet
    expect(screen.queryByRole("dialog", { name: "new session" })).not.toBeInTheDocument();

    // Click the Sidebar "New session" button. This triggers onNew, then openNewSession, then sets newSessionOpen to true.
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

    // Select the repo. The new dialog defaults to worktree=true, new-branch mode.
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

    // createWorkspace must have been called with the 6-arg signature:
    // agent="claude", repo="/tmp/alpha", baseRef="main" (first branch from mock), branch="feat/x", title="" (no name entered), worktree=true
    expect(createWorkspace).toHaveBeenCalledWith("claude", "/tmp/alpha", "main", expect.stringMatching(/^[A-Za-z0-9._\/-]+$/), "", true);

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
        repoPath: "/repo/repo-alpha",
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

    await screen.findByRole("button", { name: /^Alpha\b/ });
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

    // Click Create. createWorkspace rejects with a dirty-tree error.
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

  it("repo dropdown shows a friendly name for discovered repos and falls back to the raw path for workspace-derived entries", async () => {
    // fakeWorkspaces[0].worktreePath ("/tmp/alpha") has no matching RepoInfo.
    // It only enters `repos` through the workspace-derived union, so it must fall back to its raw path.
    // discoverRepos(), mocked module-wide, resolves "/discovered/repo-a" with name "repo-a" and branch "main".
    // This entry must render as a friendly label.
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([fakeWorkspaces[0]]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(screen.getByRole("button", { name: "New session" }));
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );

    const repoSelect = screen.getByLabelText(/^repo$/i) as HTMLSelectElement;
    await waitFor(() => {
      const values = Array.from(repoSelect.options).map((o) => o.value);
      expect(values).toContain("/discovered/repo-a");
    });
    const options = Array.from(repoSelect.options);
    const discovered = options.find((o) => o.value === "/discovered/repo-a");
    const fallback   = options.find((o) => o.value === "/tmp/alpha");
    expect(discovered?.textContent).toBe("repo-a · main");
    expect(fallback?.textContent).toBe("/tmp/alpha");
  });

  it("handleCreate calls onSelect with existing session id when WorkspaceForBranch returns found=true", async () => {
    const { listWorkspaces, createWorkspace, workspaceForBranch } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-existing", title: "Existing", branch: "feat/taken", state: "idle" as const,
        worktreePath: "/tmp/existing", agent: "claude", paneId: "p-existing", lastActive: "",
        repoPath: "/repo/Existing",
        caps: { approvals: false, attention: false },
      },
    ]);
    (workspaceForBranch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({ id: "ws-existing", found: true });

    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Existing\b/ });

    // Open dialog
    await fireEvent.click(screen.getByRole("button", { name: "New session" }));
    await waitFor(() => screen.getByRole("dialog", { name: "new session" }));

    // Wait for branch options (starting point) to load
    await waitFor(() => screen.getByLabelText(/starting point/i));

    // Set branch name to the taken branch name
    await fireEvent.input(screen.getByLabelText(/^branch name$/i), { target: { value: "feat/taken" } });

    // Click Create. This should trigger a resume, not a new workspace.
    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await tick();

    // workspaceForBranch was called
    expect(workspaceForBranch).toHaveBeenCalledWith(expect.any(String), "feat/taken");

    // createWorkspace was not called. The test resumed the session instead.
    expect(createWorkspace).not.toHaveBeenCalled();

    // Dialog closed
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "new session" })).not.toBeInTheDocument()
    );

    // The resume preview appears for the existing session. Confirm to open it.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
  });
});

describe("App.svelte ConfirmDialog (workspace remove)", () => {
  it("session:remove command shows ConfirmDialog; confirming hides workspace + shows undo toast (no immediate removeWorkspace)", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        repoPath: "/repo/repo-alpha",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select ws-1 to make it active (required for session:remove to find active)
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
      expect(screen.queryByRole("button", { name: /^Alpha\b/ })).not.toBeInTheDocument()
    );

    // Undo toast appears
    await waitFor(() =>
      expect(screen.getByTestId("undo-toast")).toBeInTheDocument()
    );

    // removeWorkspace must NOT have been called yet
    expect(removeWorkspace).not.toHaveBeenCalled();
  });
});

describe("App.svelte DragDrop", () => {
  it("an OS file drop onto the agent terminal routes @path bytes to that pane via writeToPty", async () => {
    const { listWorkspaces, writeToPty } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        repoPath: "/repo/repo-alpha",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { layout } = await import("./lib/stores/layout.svelte");
    // App's registered Wails OnFileDrop handler calls routeOsFileDrop.
    // It passes the absolute paths that WebKitGTK delivers out of band.
    // The DOM drop event itself carries no path, so this test drives the real routing path directly.
    const { routeOsFileDrop } = await import("./lib/osFileDrop");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select ws-1 and go to agent view
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("agent");
    await tick();

    // The DragDrop drop-zone wrapping the terminal tags itself with the pane id.
    const dropZone = await screen.findByRole("region", { name: "drop zone" });
    expect(dropZone.getAttribute("data-drop-pane")).toBe("p1");

    // The drop point resolves to that drop-zone; route the native absolute path.
    const origEFP = document.elementFromPoint;
    document.elementFromPoint = () => dropZone;
    try {
      await routeOsFileDrop(10, 20, ["/tmp/alpha/foo.ts"]);
    } finally {
      document.elementFromPoint = origEFP;
    }

    // writeToPty is called with the paneId and bytes encoding the shell-quoted
    // @mention "@'/tmp/alpha/foo.ts' " (single-quoted so a path with spaces
    // survives as one token).
    await waitFor(() => {
      expect(writeToPty).toHaveBeenCalled();
      const [calledPaneId, calledBytes] = (writeToPty as ReturnType<typeof vi.fn>).mock.calls[0];
      expect(calledPaneId).toBe("p1");
      const decoded = new TextDecoder().decode(new Uint8Array(calledBytes));
      expect(decoded).toBe("@'/tmp/alpha/foo.ts' ");
    });
  });
});

// ---------------------------------------------------------------------------
// Full NORMAL keymap + mode state machine
// ---------------------------------------------------------------------------

describe("App.svelte keymap: j/k navigation", () => {
  it("j moves activeId DOWN through the workspace list (clamp at end); k moves UP (clamp at start)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });
    await tick();

    // No session is active at first. j selects the first one.
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveAttribute("aria-current", "page")
    );

    // Press j again. It selects Beta.
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveAttribute("aria-current", "page")
    );

    // Press j again at the end. It stays on Beta, because the selection clamps.
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toHaveAttribute("aria-current", "page")
    );

    // Press k. It selects Alpha.
    await fireEvent.keyDown(document.body, { key: "k" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveAttribute("aria-current", "page")
    );

    // Press k at the start. It stays on Alpha, because the selection clamps.
    await fireEvent.keyDown(document.body, { key: "k" });
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveAttribute("aria-current", "page")
    );

    // j/k must NEVER call openWorkspace
    expect(openWorkspace).not.toHaveBeenCalled();
  });
});

describe("App.svelte keymap: Enter opens focused session", () => {
  it("Enter with activeId calls openWorkspace(activeId)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });
    await tick();

    // Select Alpha via j
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    // Reset the mock call count. A click earlier in this test may have already called it.
    vi.mocked(openWorkspace).mockClear();

    // Press Enter. It opens the session.
    await fireEvent.keyDown(document.body, { key: "Enter" });
    await tick();
    expect(openWorkspace).toHaveBeenCalledWith("ws-1");
  });

  // F13: Enter on an already-live session must be a no-op.
  // A second openWorkspace call would displace the running pty, send SIGKILL to the agent, and blank the xterm.
  it("Enter on a LIVE session does NOT re-open it (F13)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });

    // Open Alpha through the resume-preview flow. It is now live, in openIds.
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    vi.mocked(openWorkspace).mockClear();

    // Press Enter again on the live, active session. The guard blocks a respawn.
    await fireEvent.keyDown(document.body, { key: "Enter" });
    await tick();
    expect(openWorkspace).not.toHaveBeenCalled();
  });
});

describe("App.svelte keymap: g-prefix sequences", () => {
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

describe("App.svelte keymap: Ctrl-` toggles shell", () => {
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

describe("App.svelte keymap: filter UI", () => {
  it("'/' shows filter input; typing filters Sidebar items; Esc hides it and restores full list", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });
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
    expect(screen.getByRole("button", { name: /^Alpha\b/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Beta\b/ })).toBeInTheDocument();

    // Type "alph". Only Alpha should remain.
    await fireEvent.input(filterInput, { target: { value: "alph" } });
    await tick();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /^Beta\b/ })).not.toBeInTheDocument();
    });

    // Esc hides filter and restores full list
    await fireEvent.keyDown(filterInput, { key: "Escape" });
    await tick();
    await waitFor(() => {
      expect(screen.queryByRole("textbox", { name: "filter sessions" })).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /^Beta\b/ })).toBeInTheDocument();
    });
  });
});

describe("App.svelte keymap: mode transitions", () => {
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

  it("Ctrl-K in NORMAL → mode becomes 'command' (command palette shortcut)", async () => {
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
    // The mode must stay "normal". Ctrl-K must not fire inside an input.
    expect(mode.current).toBe("normal");
  });
});

describe("App.svelte keymap: TERMINAL leave sequence", () => {
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
    // The mode is still "terminal". pendingLeave is set, but the mode has not left yet.
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
    await screen.findByRole("button", { name: /^Alpha\b/ });
    await tick();

    layout.setView("agent");
    mode.enterTerminal();

    const activeIdBefore = null; // activeId starts null

    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();

    // View must be unchanged
    expect(layout.view).toBe("agent");
    // No workspace became active
    expect(screen.queryByRole("button", { name: /^Alpha\b/ })?.getAttribute("aria-current")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// New command-registry entries + bell-opens-hub + unread badge
// ---------------------------------------------------------------------------

describe("App.svelte session:close command", () => {
  it("dispatching session:close calls closeWorkspace(active.id) but does NOT remove the workspace from the list", async () => {
    const { listWorkspaces, closeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        repoPath: "/repo/repo-alpha",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Dispatch session:close through the MenuBar. Open the Session menu, then click Close session.
    const sessionMenu = screen.getByRole("menuitem", { name: "Session" });
    await fireEvent.click(sessionMenu);
    await tick();
    const closeItem = screen.getByRole("menuitem", { name: "Close session" });
    await fireEvent.click(closeItem);
    await tick();

    expect(closeWorkspace).toHaveBeenCalledWith("ws-1");
    // Workspace must still be in the sidebar list
    expect(screen.getByRole("button", { name: /^Alpha\b/ })).toBeInTheDocument();
    // activeId is KEPT after close (no return-to-home), so the session left the
    // open set and the in-pane "session ended / Reopen" overlay is reachable.
    await waitFor(() => expect(screen.getByTestId("pane-ended")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Reopen" })).toBeInTheDocument();
  });

  it("clicking Reopen on the ended pane routes through openSession → openWorkspace and hides the overlay", async () => {
    const { listWorkspaces, closeWorkspace, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    vi.mocked(openWorkspace).mockClear();

    // Close so the ended-pane overlay appears.
    const sessionMenu = screen.getByRole("menuitem", { name: "Session" });
    await fireEvent.click(sessionMenu);
    await tick();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Close session" }));
    await tick();
    await waitFor(() => expect(closeWorkspace).toHaveBeenCalledWith("ws-1"));
    await waitFor(() => expect(screen.getByTestId("pane-ended")).toBeInTheDocument());

    // Reopen respawns the pty via openSession.
    await fireEvent.click(screen.getByRole("button", { name: "Reopen" }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
    // Overlay gone once the session is back in the open set.
    await waitFor(() => expect(screen.queryByTestId("pane-ended")).not.toBeInTheDocument());
  });

  it("F32: onAgentEvent state:'exited' ends the session — Reopen overlay shown, openIds dropped, approvals pruned, sidebar shows 'exited'", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "running" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: true, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha so it is live (in openIds) and the agent view is mounted.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Give it a pending approval card (must be pruned when the agent exits).
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-1", tool: "bash", summary: "Run migration" } });
    await tick();
    await waitFor(() => expect(screen.getByText("Run migration")).toBeInTheDocument());

    // The agent process exits, for example from a crash or an out-of-memory kill, while its login shell survives.
    // F32 emits an agent-event "exited" for this case. There is no pty:exit event.
    cb({ workspaceId: "ws-1", kind: "state", state: "exited", err: "exited (code 137)" });
    await tick();

    // Session ended: the existing "session ended / Reopen" overlay shows (openIds dropped).
    await waitFor(() => expect(screen.getByTestId("pane-ended")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Reopen" })).toBeInTheDocument();
    // The dead session's approval card is pruned.
    expect(screen.queryByText("Run migration")).not.toBeInTheDocument();
    // The sidebar surfaces the distinct terminal state "exited", not a red "error".
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("exited")
    );
    expect(screen.getByRole("button", { name: /^Alpha\b/ })).not.toHaveTextContent("error");
  });

  it("B1: a live agent event self-heals the openIds latch after a stale 'exited' re-latches the ended overlay", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "running" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha so it is live (in openIds) and the agent view is mounted.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const cb = captured.agent.at(-1)!;

    // The agent process exits. The "session ended / Reopen" overlay latches on.
    cb({ workspaceId: "ws-1", kind: "state", state: "exited", err: "exited (code 137)" });
    await waitFor(() => expect(screen.getByTestId("pane-ended")).toBeInTheDocument());

    // Click Reopen. openSession respawns the pty and adds ws-1 back to openIds.
    await fireEvent.click(screen.getByRole("button", { name: "Reopen" }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
    // A live "running" event arrives after the reopen.
    cb({ workspaceId: "ws-1", kind: "state", state: "running" });
    await waitFor(() => expect(screen.queryByTestId("pane-ended")).not.toBeInTheDocument());

    // A second, stale "exited" event arrives. This is an old shell's exit sentinel firing during the reopen teardown.
    // It re-latches the overlay, because openIds has no self-heal of its own.
    // This documents the one-way latch.
    cb({ workspaceId: "ws-1", kind: "state", state: "exited", err: "stale exit" });
    await waitFor(() => expect(screen.getByTestId("pane-ended")).toBeInTheDocument());

    // The next live agent event proves the pty exists and heals the latch.
    // The overlay clears with no further Reopen click. Without the B1 heal, this stays lit.
    cb({ workspaceId: "ws-1", kind: "state", state: "running" });
    await waitFor(() => expect(screen.queryByTestId("pane-ended")).not.toBeInTheDocument());
  });
});

// ---------------------------------------------------------------------------
// B3: general Ctrl-Shift-C copy on non-terminal surfaces (WebKit2GTK-native clipboard)
// ---------------------------------------------------------------------------
describe("App.svelte general Ctrl-Shift-C copy for non-terminal surfaces", () => {
  it("copies a non-terminal page selection through clipboardSetText", async () => {
    const { listWorkspaces, clipboardSetText } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });

    // A non-empty selection somewhere on the page (diff view / dialog / general text).
    const getSel = vi.spyOn(window, "getSelection").mockReturnValue({
      toString: () => "selected page text",
    } as unknown as Selection);
    try {
      // The target is not inside a terminal zone, so the copy route fires.
      await fireEvent.keyDown(document.body, { key: "c", ctrlKey: true, shiftKey: true });
      await waitFor(() =>
        expect(clipboardSetText).toHaveBeenCalledWith("selected page text")
      );
    } finally {
      getSel.mockRestore();
    }
  });

  it("does NOT copy when the keydown originates inside a [data-terminal-zone]", async () => {
    const { listWorkspaces, clipboardSetText } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha so its agent terminal zone (data-terminal-zone) is mounted.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const zone = document.querySelector("[data-terminal-zone]") as HTMLElement;
    expect(zone).not.toBeNull();

    const getSel = vi.spyOn(window, "getSelection").mockReturnValue({
      toString: () => "selected terminal text",
    } as unknown as Selection);
    try {
      vi.mocked(clipboardSetText).mockClear();
      // The target is inside a terminal zone, so the terminal owns the copy.
      // The app-level route must skip it, and never double-handle the terminal's own selection.
      await fireEvent.keyDown(zone, { key: "c", ctrlKey: true, shiftKey: true });
      await tick();
      expect(clipboardSetText).not.toHaveBeenCalled();
    } finally {
      getSel.mockRestore();
    }
  });
});

// ---------------------------------------------------------------------------
// BUG B: bottom + home shell drawers are terminal zones (click keeps TERMINAL)
// ---------------------------------------------------------------------------
describe("App.svelte shell-drawer wrappers are terminal zones", () => {
  it("session shell-drawer wrapper has data-terminal-zone and enters terminal mode on pointerdown", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const shellZone = document.querySelector("[data-zone='shell-drawer']") as HTMLElement;
    // Wrapper must be inside a terminal zone (the app-root pointerdown guard leaves it alone).
    expect(shellZone.closest("[data-terminal-zone]")).not.toBeNull();

    // From NORMAL, a pointerdown on the drawer must switch to TERMINAL mode.
    (mode as any).current = "normal";
    await fireEvent.pointerDown(shellZone);
    expect(mode.current).toBe("terminal");
  });

  it("home shell-drawer wrapper has data-terminal-zone and enters terminal mode on pointerdown", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]); // No session means the home view shows.
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    // home-shell-zone renders under {#if homeShellCwdValue}, set by the async onMount.
    await waitFor(() => expect(document.querySelector(".home-shell-zone")).not.toBeNull());

    const homeZone = document.querySelector(".home-shell-zone") as HTMLElement;
    expect(homeZone.closest("[data-terminal-zone]")).not.toBeNull();

    (mode as any).current = "normal";
    await fireEvent.pointerDown(homeZone);
    expect(mode.current).toBe("terminal");
  });
});

describe("App.svelte worktree:open command", () => {
  const oneWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: false, attention: false },
    },
  ];

  async function dispatchWorktreeOpen() {
    // F51: the former "Worktree" top-level menu was folded into "Session" and
    // "Open worktree" renamed to "Open session" (the command id is unchanged).
    const sessionMenu = screen.getByRole("menuitem", { name: "Session" });
    await fireEvent.click(sessionMenu);
    await tick();
    const openItem = screen.getByRole("menuitem", { name: "Open session" });
    await fireEvent.click(openItem);
    await tick();
  }

  // F13: reopening a LIVE session displaces its backend pty (SIGKILLs the running
  // agent's process group) and re-types the launch command over a blanked xterm.
  // worktree:open must guard on liveness, exactly like the Enter key and onSelect.
  it("dispatching worktree:open on a LIVE session does NOT re-open it (F13)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    // ws-1 is now live (in openIds). Clear the open call from the initial open.
    vi.mocked(openWorkspace).mockClear();

    await dispatchWorktreeOpen();

    // The guard skips the already-live session. There is no respawn.
    expect(openWorkspace).not.toHaveBeenCalled();
  });

  it("dispatching worktree:open on a DEAD active session reopens it (F13)", async () => {
    const { listWorkspaces, openWorkspace, closeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Close so ws-1 is active-but-dead (left openIds, activeId kept).
    await fireEvent.click(screen.getByRole("menuitem", { name: "Session" }));
    await tick();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Close session" }));
    await waitFor(() => expect(closeWorkspace).toHaveBeenCalledWith("ws-1"));
    vi.mocked(openWorkspace).mockClear();

    await dispatchWorktreeOpen();

    // A dead session IS reopened by the command.
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
  });
});

describe("App.svelte agent:approve-all / deny-all", () => {
  const twoApprovalWorkspaces = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "awaiting-approval" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: true, attention: false },
    },
    {
      id: "ws-2", title: "Beta", branch: "feat/beta", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      repoPath: "/repo/repo-beta",
      caps: { approvals: true, attention: false },
    },
  ];

  // "Approve all pending" resolves only the ACTIVE session's pending requests.
  // The card the user is looking at belongs to the active session, so a batch
  // there must NOT reach into a backgrounded session and silently approve its
  // tools. With Alpha active, approve-all resolves Alpha's request only; Beta's
  // stays pending until the user views Beta and batches there.
  it("agent:approve-all resolves ONLY the active session's pending approvals", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha (ws-1) so it is the ACTIVE workspace.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

    // Only Alpha (active) is approved; Beta (backgrounded) is NOT touched.
    await waitFor(() => expect(approve).toHaveBeenCalledWith("req-a1", "allow"));
    expect(approve).not.toHaveBeenCalledWith("req-b1", "allow");

    // Alpha (active) approval cleared.
    await waitFor(() =>
      expect(screen.queryByText("Alpha approval")).not.toBeInTheDocument()
    );

    // Beta's approval is still pending. Switch to it and check it is shown.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    await waitFor(() => expect(screen.getByText("Beta approval")).toBeInTheDocument());
  });

  it("agent:approve-all: a per-item failure keeps ONLY that item; the rest still resolve", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWorkspaces);
    // Both requests belong to the ACTIVE session (approve-all is active-scoped).
    // Only the first approve call, req-fail, enqueued first, rejects. The second call, req-ok, succeeds.
    // approve-all must not abort the rest of the queue after the failure.
    vi.mocked(approve).mockRejectedValueOnce(new Error("network error"));
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha (ws-1) so it is the ACTIVE workspace.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const { getItems } = await import("./lib/stores/notifications.svelte");
    const notifBefore = getItems().length;

    // TWO requests, both on the active session (ws-1): the first fails, the second resolves.
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-fail", tool: "bash", summary: "Will fail" } });
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-ok", tool: "bash", summary: "Should resolve" } });
    await tick();

    // Dispatch approve-all
    const agentMenu = screen.getByRole("menuitem", { name: "Agent" });
    await fireEvent.click(agentMenu);
    await tick();
    const approveAllItem = screen.getByRole("menuitem", { name: "Approve all pending" });
    await fireEvent.click(approveAllItem);

    // Both were attempted (the failure did not abort the batch).
    await waitFor(() => {
      expect(approve).toHaveBeenCalledWith("req-fail", "allow");
      expect(approve).toHaveBeenCalledWith("req-ok", "allow");
    });

    // A blocking notification was added for the failed item.
    await waitFor(() => {
      const items = getItems();
      expect(items.some(n => n.title === "Approval failed")).toBe(true);
    });
    expect(getItems().length).toBeGreaterThan(notifBefore);

    // req-fail was not cleared. Its summary is still the shown head on the active card.
    await waitFor(() =>
      expect(screen.getByText("Will fail")).toBeInTheDocument()
    );
    // req-ok did resolve. The queue dropped from 2 to 1.
    // The batch buttons, which render only when the active queue has more than 1 pending, are gone.
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /approve all/i })).not.toBeInTheDocument()
    );
  });
});

describe("App.svelte notifications:open toggles hub", () => {
  it("hub not in DOM initially; clicking bell shows it; clicking again hides it", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    // Hub must NOT be in DOM initially
    expect(screen.queryByRole("region", { name: "notification hub" })).not.toBeInTheDocument();

    // Click the bell. The hub opens.
    const bell = screen.getByRole("menuitem", { name: "notifications" });
    await fireEvent.click(bell);
    await tick();
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "notification hub" })).toBeInTheDocument()
    );

    // Click again. The hub closes.
    await fireEvent.click(bell);
    await tick();
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "notification hub" })).not.toBeInTheDocument()
    );
  });
});

describe("App.svelte unread badge on MenuBar bell", () => {
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
// HelpDialog opened by help:shortcuts / help:about menu commands
// ---------------------------------------------------------------------------

describe("App.svelte HelpDialog opens via help:shortcuts command", () => {
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
// Approval batching: "Approve all / Deny all" buttons
// ---------------------------------------------------------------------------

describe("App.svelte approval batch buttons", () => {
  const twoApprovalWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "awaiting-approval" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: true, attention: false },
    },
    {
      id: "ws-2", title: "Beta", branch: "feat/beta", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      repoPath: "/repo/repo-beta",
      caps: { approvals: true, attention: false },
    },
  ];

  it("with TWO pending approvals on the ACTIVE session: batch buttons render; Approve all resolves both", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(twoApprovalWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Two pending requests on Alpha (ws-1), the ACTIVE session, plus one on Beta
    // to prove the batch stays active-scoped (Beta's is never touched).
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-a1", tool: "bash", summary: "Alpha task 1" } });
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-a2", tool: "bash", summary: "Alpha task 2" } });
    cb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-b", tool: "bash", summary: "Beta task" } });
    await tick();

    // Select Alpha so the approval card appears (active.id = ws-1, approvals[ws-1] exists)
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // The active session's queue, with 2 pending, drives the batch-button render.
    // The "N pending" indicator counts only this session's queue.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /approve all/i })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: /deny all/i })).toBeInTheDocument();
    });

    // Clicking Approve all resolves BOTH of the active session's requests, but NOT
    // the backgrounded Beta request.
    await fireEvent.click(screen.getByRole("button", { name: /approve all/i }));
    await waitFor(() => {
      expect(approve).toHaveBeenCalledWith("req-a1", "allow");
      expect(approve).toHaveBeenCalledWith("req-a2", "allow");
    });
    expect(approve).not.toHaveBeenCalledWith("req-b", "allow");

    // Beta's approval is still pending. Switch to it and confirm it is shown.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    await waitFor(() => expect(screen.getByText("Beta task")).toBeInTheDocument());
  });

  it("with ONE pending approval: batch buttons do NOT render", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([twoApprovalWs[0]]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-solo", tool: "bash", summary: "Solo task" } });
    await tick();
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
// selection to agent through onSendToAgent
// ---------------------------------------------------------------------------

describe("App.svelte sendToAgent wires Editor→writeToPty", () => {
  const codeWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

    // Do not select any workspace. There is no active session.
    await screen.findByRole("button", { name: /^Alpha\b/ });
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
// Repo discovery + first-run empty state
// ---------------------------------------------------------------------------

describe("App.svelte discoverRepos called on dialog open; discovered repos appear in dialog", () => {
  it("opening the New Session dialog calls discoverRepos and discovered path appears as an option", async () => {
    const { listWorkspaces, discoverRepos } = await import("./lib/wails");
    // This is a fresh install, with no existing workspaces.
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
      expect(screen.queryByRole("button", { name: /^Alpha\b/ })).not.toBeInTheDocument()
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
// Deferred removal + undo toast
// ---------------------------------------------------------------------------

describe("App.svelte deferred remove hides workspace + shows undo toast without calling removeWorkspace", () => {
  const removeWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: false, attention: false },
    },
  ];

  it("confirming remove hides workspace from sidebar and shows undo toast; removeWorkspace NOT called", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(removeWs);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Trigger remove through the Sidebar context. Open the command palette and run session:remove.
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
      expect(screen.queryByRole("button", { name: /^Alpha\b/ })).not.toBeInTheDocument()
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

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toBeInTheDocument()
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

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
// independent secondary pane (splitId)
// ---------------------------------------------------------------------------

describe("App.svelte split secondary pane", () => {
  it("split=true + splitId=ws-2 → secondary pane shows Beta terminal (paneId p2), primary shows Alpha (paneId p1)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Select Alpha as active
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

// --- Behavior 5: session to split through a stage-level drop ---
describe("App.svelte drag-to-split (behavior 5)", () => {
  it("dropping a session id onto the stage sets layout.split=true and layout.splitId to that id", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setSplitId");
    const { default: App } = await import("./App.svelte");
    render(App);
    // Select a workspace so the stage is active
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session. (even though no workspace content is needed here,
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
    await screen.findByRole("button", { name: /^Alpha\b/ });

    // Simulate dragging ws-2 onto ws-1 in the Sidebar
    // The workspace list rows are <li draggable> elements
    const betaBtn  = screen.getByRole("button", { name: /^Beta\b/ });
    const betaLi   = betaBtn.closest("li") as HTMLElement;
    const alphaBtn = screen.getByRole("button", { name: /^Alpha\b/ });
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
// FileTree "@mention:" prefix routed to sendToAgent (not codePath)
// ---------------------------------------------------------------------------
describe("App.svelte: FileTree @mention prefix routes to sendToAgent", () => {
  const codeWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: false, attention: false },
    },
  ];

  it("FileTree onOpen with '@mention:/some/file.ts' sends '@/some/file.ts ' via writeToPty, not readFile", async () => {
    const { listWorkspaces, writeToPty, readFile } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(codeWs);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

    // The @mention path calls writeToPty with "@/some/file.ts ". It has a leading '@' and a trailing space.
    await waitFor(() => {
      expect(writeToPty).toHaveBeenCalledTimes(1);
      const [paneId, bytes] = vi.mocked(writeToPty).mock.calls[0];
      expect(paneId).toBe("p1");
      expect(new TextDecoder().decode(new Uint8Array(bytes as number[]))).toBe("@/some/file.ts ");
    });

    // readFile must not be called. @mention does not set codePath.
    expect(readFile).not.toHaveBeenCalled();
  });

  it("FileTree onOpen without '@mention:' prefix updates codePath, does NOT call writeToPty", async () => {
    const { listWorkspaces, writeToPty } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(codeWs);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
// onDecision deletes by owning workspace, not activeId
// ---------------------------------------------------------------------------
describe("App.svelte: onDecision keys deletion by reqId owner, not activeId", () => {
  it("allow on ws-2 card while active switches to ws-1 mid-await: only ws-2 cleared (race-proof)", async () => {
    // This test exercises the async race:
    //   1. Beta is active. Click Allow on req-ws2. approve() is deferred and does not resolve yet.
    //   2. While waiting, click Alpha. activeId becomes ws-1.
    //   3. Resolve approve(). onDecision finishes.
    //   Buggy code: it deletes approvals[activeId], which is approvals["ws-1"]. Alpha's task disappears. This is wrong.
    //   Fixed code: it deletes approvals[owner("req-ws2")], which is approvals["ws-2"]. Beta's task disappears, and Alpha's task stays intact.
    const { listWorkspaces, approve } = await import("./lib/wails");

    // Deferred approve: caller controls when the promise resolves
    let resolveApprove!: () => void;
    vi.mocked(approve).mockImplementation(
      () => new Promise<void>((res) => { resolveApprove = res; })
    );

    const ws1 = {
      id: "ws-1", title: "Alpha", branch: "main", state: "awaiting-approval" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: true, attention: false },
    };
    const ws2 = {
      id: "ws-2", title: "Beta", branch: "feat", state: "awaiting-approval" as const,
      worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
      repoPath: "/repo/repo-beta",
      caps: { approvals: true, attention: false },
    };
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([ws1, ws2]);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Inject approvals for both workspaces
    await screen.findByRole("button", { name: /^Alpha\b/ });
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-ws1", tool: "bash", summary: "Alpha task" } });
    cb({ workspaceId: "ws-2", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-ws2", tool: "bash", summary: "Beta task" } });
    await tick();

    // Step 1: activate Beta via preview confirm, confirm its card is visible
    const betaBtn = screen.getByRole("button", { name: /^Beta\b/ });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    await waitFor(() => expect(screen.getByText("Beta task")).toBeInTheDocument());

    // Step 2: click Allow on Beta's card. approve() is called but not resolved yet.
    const allowBtn = screen.getByRole("button", { name: "Allow" });
    await fireEvent.click(allowBtn);
    await tick();
    expect(approve).toHaveBeenCalledWith("req-ws2", "allow");

    // Step 3: switch active workspace to Alpha via preview confirm BEFORE approve resolves
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
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
// session:close cleans up approvals/fsVersion
// ---------------------------------------------------------------------------
describe("App.svelte: session:close cleans up per-workspace frontend state", () => {
  it("after closeWorkspace resolves, approvals/fsVersion for that id are removed", async () => {
    const { listWorkspaces, closeWorkspace } = await import("./lib/wails");
    (closeWorkspace as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        repoPath: "/repo/repo-alpha",
        caps: { approvals: true, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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
// gt/gT view cycling
// ---------------------------------------------------------------------------
describe("App.svelte: g-prefix gt/gT cycles views", () => {
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
// Non-blocking tiers stay unread until the hub opens (F42a/F42b).
// The hub is a docked panel, not a transient toast.
// So an unseen ambient or routine event must not tick the unread badge down on a timer.
// It stays unread until the user opens the hub, through markAllRead.
// The old 3s and 6s auto-dismiss timers were removed.
// ---------------------------------------------------------------------------
describe("notifications.svelte.ts: non-blocking tiers stay unread until the hub opens", () => {
  it("ambient notification stays UNREAD past the old 6s window; markAllRead clears it (F42a)", async () => {
    vi.useFakeTimers();
    try {
      const { addAmbient, getItems, markAllRead } = await import("./lib/stores/notifications.svelte");
      const before = getItems().length;

      addAmbient("ws-1", "Done", "Build succeeded");
      const items = getItems();
      expect(items.length).toBe(before + 1);
      const n = items.find((x) => x.title === "Done")!;
      expect(n).toBeDefined();
      expect(n.read).toBe(false);

      // Advance well past the old 6s auto-dismiss window. With the timer removed, the item must remain unread while the hub is closed.
      // There is no silent tick-down.
      vi.advanceTimersByTime(60000);
      await tick();
      expect(getItems().find((x) => x.id === n.id)?.read).toBe(false);

      // Opening the hub (markAllRead) is the ONLY thing that clears it.
      markAllRead();
      expect(getItems().find((x) => x.id === n.id)?.read).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it("blocking notification is NEVER auto-dismissed", async () => {
    vi.useFakeTimers();
    try {
      const { addBlocking, getItems } = await import("./lib/stores/notifications.svelte");

      addBlocking("ws-1", "Error", "Something broke");
      const n = getItems().find((x) => x.title === "Error")!;
      expect(n).toBeDefined();

      // Advance a long time. Blocking stays unread until the hub opens.
      vi.advanceTimersByTime(60000);
      await tick();
      expect(getItems().find((x) => x.id === n.id)?.read).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("routine notification stays UNREAD past the old 3s window; markAllRead clears it (F42a)", async () => {
    vi.useFakeTimers();
    try {
      const { addRoutine, getItems, markAllRead } = await import("./lib/stores/notifications.svelte");

      addRoutine("ws-1", "Info", "Synced");
      const n = getItems().find((x) => x.title === "Info")!;
      expect(n).toBeDefined();
      expect(n.read).toBe(false);

      vi.advanceTimersByTime(60000);
      await tick();
      expect(getItems().find((x) => x.id === n.id)?.read).toBe(false);

      markAllRead();
      expect(getItems().find((x) => x.id === n.id)?.read).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });
});

// ---------------------------------------------------------------------------
// diffstat counts: Sidebar row +/− and status-line diffstat
// ---------------------------------------------------------------------------
describe("App.svelte diffstat counts in Sidebar and status line", () => {
  it("Sidebar row for /tmp/alpha shows +5 and −2 after workspaces load", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Wait for workspaces to load and diffStats to be computed
    await screen.findByRole("button", { name: /^Alpha\b/ });

    // diffStat for /tmp/alpha returns 2 files, for a total of +5 and -2.
    await waitFor(() => {
      const alphaBtn = screen.getByRole("button", { name: /^Alpha\b/ });
      expect(alphaBtn.textContent).toContain("+5");
      expect(alphaBtn.textContent).toContain("2");
    });
  });

  it("Sidebar row for /tmp/beta has NO diffstat span (diffStat returns [])", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: /^Beta\b/ });
    // Allow time for diffstat to settle; Beta gets [] so no span should appear
    await tick();
    await tick();

    const betaBtn = screen.getByRole("button", { name: /^Beta\b/ });
    expect(betaBtn.querySelector(".sidebar-diffstat")).toBeNull();
  });

  it("status line shows active workspace diffstat when Alpha is selected", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Alpha's diffStat returns 2 files. The goal-gradient pill reads "2 files to review".
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
    await screen.findByRole("button", { name: /^Alpha\b/ });
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
// sidebar collapse via Ctrl-b and toggle rail button
// ---------------------------------------------------------------------------
describe("App.svelte sidebar collapse via Ctrl-b and toggle rail", () => {
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

    // Press Ctrl-b again. It goes back to false.
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
// workspace attach routing via onWorkspaceAttach
// ---------------------------------------------------------------------------
describe("App.svelte onWorkspaceAttach routes to matching workspace", () => {
  it("attach callback with query matching a workspace title shows resume preview for that workspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Wait for workspaces to load (onWorkspaceAttach is subscribed in onMount)
    await screen.findByRole("button", { name: /^Alpha\b/ });
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

    await screen.findByRole("button", { name: /^Beta\b/ });
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

    await screen.findByRole("button", { name: /^Alpha\b/ });
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
    // Wails marshals Go errors to strings. Reject with a bare string, exactly like production does.
    (removeWorkspace as ReturnType<typeof vi.fn>).mockRejectedValue("worktree has uncommitted changes");
    (forceRemoveWorkspace as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => screen.getByText("Alpha"));

    // 1. Select Alpha and trigger requestRemove through the command palette's session:remove.
    const alphaBtn = screen.getByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    // The resume preview appears. Confirm to open the session.
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

describe("App.svelte home shell mount persistence (4.4c)", () => {
  it("home shell stays mounted (shell-home) after navigating into a session", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App, {});
    await waitFor(() => screen.getByText("Alpha"));
    const homeOnHome = screen.getAllByTestId("shell-drawer-probe").filter(p => p.getAttribute("data-pane-id") === "shell-home");
    expect(homeOnHome.length).toBe(1); // present on home
    // open a session via the 4.2 resume-preview flow
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
    const openBtn = await waitFor(() => screen.getByRole("button", { name: /^open$/i }));
    await fireEvent.click(openBtn);
    await waitFor(() => expect(openWorkspace).toHaveBeenCalled());
    // home shell-home probe STILL mounted (would be 0 if it were still inside {:else})
    const homeAfter = screen.getAllByTestId("shell-drawer-probe").filter(p => p.getAttribute("data-pane-id") === "shell-home");
    expect(homeAfter.length).toBe(1);
    expect(screen.queryByText(/welcome to perch/i)).toBeNull();
  });
});

describe("App.svelte home-view persistent shell (4.4b)", () => {
  it("home view renders welcome card AND a ShellDrawer with paneId shell-home", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]); // No sessions means the home view shows.
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
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
    // The resume preview appears. Confirm with Open (the 4.2 flow).
    const openBtn = await waitFor(() => screen.getByRole("button", { name: /^open$/i }));
    await fireEvent.click(openBtn);
    await waitFor(() => expect(openWorkspace).toHaveBeenCalled());
    expect(screen.queryByText(/welcome to perch/i)).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Issue A: stale banner / CleanupPanel refresh after cleanup
// ---------------------------------------------------------------------------
describe("App.svelte CleanupPanel onClose refetches stale sessions", () => {
  const staleSession = {
    id: "ws-old", title: "old", branch: "feat/old", agent: "claude",
    lastActive: new Date(0).toISOString(), added: 0, removed: 0,
    clean: true, merged: true, safe: true,
  };

  it("listStaleSessions is called again after Remove selected completes (banner count refreshed)", async () => {
    const { listStaleSessions, cleanupSessions } = await import("./lib/wails");
    // First call (onMount): returns one stale session so the banner appears.
    // Subsequent calls (after cleanup): return [] so the banner should disappear.
    (listStaleSessions as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([staleSession])
      .mockResolvedValue([]);

    const { default: App } = await import("./App.svelte");
    render(App, {});

    // Banner must appear
    await waitFor(() =>
      expect(screen.getByTestId("stale-banner")).toBeInTheDocument()
    );
    const callsBefore = (listStaleSessions as ReturnType<typeof vi.fn>).mock.calls.length;

    // Open the CleanupPanel via the Review button
    await fireEvent.click(screen.getByRole("button", { name: /review/i }));
    await tick();

    // The panel is open. The "Remove selected" button is present, because 1 safe session is checked.
    const removeBtn = await screen.findByRole("button", { name: /remove selected/i });
    expect(removeBtn).toBeInTheDocument();
    expect(removeBtn).not.toBeDisabled();

    // Click Remove selected. The ConfirmDialog appears.
    await fireEvent.click(removeBtn);
    await tick();
    // Confirm
    const confirmBtn = await screen.findByRole("button", { name: /^remove$/i });
    await fireEvent.click(confirmBtn);
    await tick();

    // cleanupSessions must have been called
    await waitFor(() => expect(cleanupSessions).toHaveBeenCalled());

    // listStaleSessions must have been called again (refetch on close)
    await waitFor(() => {
      expect((listStaleSessions as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThan(callsBefore);
    });

    // Banner must be gone (refetch returned [])
    await waitFor(() =>
      expect(screen.queryByTestId("stale-banner")).not.toBeInTheDocument()
    );
  });
});

describe("App.svelte staleSessions null-safety (nil-slice guard)", () => {
  it("does not crash and shows no stale-banner when listStaleSessions resolves null", async () => {
    const { listStaleSessions } = await import("./lib/wails");
    (listStaleSessions as ReturnType<typeof vi.fn>).mockResolvedValue(null);
    const { default: App } = await import("./App.svelte");
    render(App);
    // Wait for onMount to complete. Any throw would surface here.
    await waitFor(() => expect(document.querySelector("[data-zone='sidebar']")).toBeInTheDocument());
    // staleSessions should be treated as an empty array, so no stale banner shows.
    expect(document.querySelector('[data-testid="stale-banner"]')).not.toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Approval QUEUE: a second approval on the same session must not overwrite the
// first (the dropped one would hang its agent hook forever).
// ---------------------------------------------------------------------------
describe("App.svelte approval queue (per-session)", () => {
  const queueWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "awaiting-approval" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: true, attention: false },
    },
  ];

  it("two approvals on the SAME session queue up: resolving the head reveals the next (nothing dropped)", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(queueWs);
    // A prior test may have installed a deferred approve() implementation;
    // vi.clearAllMocks() clears call history but NOT implementations. Restore the
    // resolving default so Allow actually completes and dequeues.
    vi.mocked(approve).mockImplementation(async () => {});
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Two approvals arrive for ws-1 before the user acts on the first.
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-first", tool: "bash", summary: "First tool" } });
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-second", tool: "bash", summary: "Second tool" } });
    await tick();

    // The head (First tool) is shown, with a "1 more queued for this session" note.
    await waitFor(() => expect(screen.getByText("First tool")).toBeInTheDocument());
    expect(screen.getByTestId("session-queue-note")).toHaveTextContent(/1 more queued/i);
    // Second tool is NOT shown yet (it is behind the head).
    expect(screen.queryByText("Second tool")).not.toBeInTheDocument();

    // Allow the head. approve(req-first) fires, then the second approval surfaces. Nothing is dropped.
    await fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await waitFor(() => expect(approve).toHaveBeenCalledWith("req-first", "allow"));
    await waitFor(() => expect(screen.getByText("Second tool")).toBeInTheDocument());
    expect(screen.queryByText("First tool")).not.toBeInTheDocument();
    // Only one approval is left, so the per-session queued note is gone.
    expect(screen.queryByTestId("session-queue-note")).not.toBeInTheDocument();

    // Allow the second approval. Its own approve() call fires, and the card clears.
    await fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await waitFor(() => expect(approve).toHaveBeenCalledWith("req-second", "allow"));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument());
  });

  it("a resolved approval clears the session's awaiting-approval attention (backstop)", async () => {
    const { listWorkspaces, approve } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(queueWs);
    vi.mocked(approve).mockImplementation(async () => {});
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-x", tool: "bash", summary: "Attn tool" } });
    await tick();

    // Sidebar shows the attention label while awaiting-approval.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("needs you")
    );

    // Resolve it. The backstop demotes awaiting-approval to idle locally.
    await fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveTextContent("idle")
    );
  });
});

// ---------------------------------------------------------------------------
// Modal key-trap: while an overlay is open, NORMAL/TERMINAL nav must not run
// behind it; only Escape acts (dismisses the overlay).
// ---------------------------------------------------------------------------
describe("App.svelte modal key-trap", () => {
  it("with the resume-preview open, 'j' does NOT change selection and Enter does NOT open a session", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    // Open the resume-preview for Alpha (do NOT confirm).
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());

    // 'j' behind the modal must not move the selection.
    await fireEvent.keyDown(document.body, { key: "j" });
    await tick();
    expect(screen.getByRole("button", { name: /^Beta\b/ })).not.toHaveAttribute("aria-current", "page");

    // Enter behind the modal must not open the active session (would open the WRONG one).
    await fireEvent.keyDown(document.body, { key: "Enter" });
    await tick();
    expect(openWorkspace).not.toHaveBeenCalled();

    // Escape dismisses the preview.
    await fireEvent.keyDown(document.body, { key: "Escape" });
    await tick();
    await waitFor(() => expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument());
  });

  it("with the Help dialog open, '1' does NOT switch the view", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();
    layout.setView("agent");

    // Open Help via the Help menu.
    await fireEvent.click(screen.getByRole("menuitem", { name: "Help" }));
    await tick();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Keyboard shortcuts" }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "help" })).toBeInTheDocument());

    // '1' behind the modal must NOT change the view.
    await fireEvent.keyDown(document.body, { key: "1" });
    await tick();
    expect(layout.view).toBe("agent");
  });
});

// ---------------------------------------------------------------------------
// Single-letter shortcuts must not hijack typing in editable elements.
// ---------------------------------------------------------------------------
describe("App.svelte editable-target guard", () => {
  it("pressing 'j' inside the New Session dialog's branch-name input does NOT move selection or preventDefault", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    await screen.findByRole("button", { name: /^Alpha\b/ });
    // Open the New Session dialog.
    await fireEvent.click(screen.getByRole("button", { name: "New session" }));
    await waitFor(() => expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument());
    await waitFor(() => {
      const sp = screen.getByLabelText(/starting point/i) as HTMLSelectElement;
      expect(sp.options.length).toBeGreaterThan(0);
    });

    const branchInput = screen.getByLabelText(/^branch name$/i) as HTMLInputElement;
    // A 'j' keydown targeting the input must be allowed through (not preventDefaulted).
    const ev = await fireEvent.keyDown(branchInput, { key: "j" });
    // fireEvent returns false if preventDefault was called; true otherwise.
    expect(ev).toBe(true);
    // Selection unchanged behind the dialog.
    expect(screen.getByRole("button", { name: /^Beta\b/ })).not.toHaveAttribute("aria-current", "page");
  });
});

// ---------------------------------------------------------------------------
// Resume-preview backdrop click dismisses it.
// ---------------------------------------------------------------------------
describe("App.svelte resume-preview backdrop", () => {
  it("clicking the overlay backdrop dismisses the resume-preview without opening the workspace", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    const preview = await waitFor(() => screen.getByTestId("resume-preview"));

    // Click the backdrop (the .modal-overlay ancestor), NOT the dialog itself.
    const overlay = preview.closest(".modal-overlay") as HTMLElement;
    await fireEvent.click(overlay);
    await tick();

    await waitFor(() => expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument());
    expect(openWorkspace).not.toHaveBeenCalled();

    // Clicking inside the dialog does NOT dismiss it (sanity re-open + inner click).
    await fireEvent.click(alphaBtn);
    const preview2 = await waitFor(() => screen.getByTestId("resume-preview"));
    await fireEvent.click(preview2);
    await tick();
    expect(screen.getByTestId("resume-preview")).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------
// Split picker: same session cannot be placed in both panes, and a not-open
// split session gets its pty spawned so the secondary pane is not dead.
// ---------------------------------------------------------------------------
describe("App.svelte split pane guards", () => {
  it("dropping the ACTIVE session onto the stage does NOT split into the same session", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const spy = vi.spyOn(layout, "setSplitId");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha (ws-1) as active.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("agent");
    await tick();

    const stageZone = document.querySelector("[data-zone='stage']") as HTMLElement;
    const store = new Map<string, string>([["application/x-perch-session", "ws-1"]]);
    const dt = {
      setData: vi.fn(),
      getData: (type: string) => store.get(type) ?? "",
      types: ["application/x-perch-session"],
      files: [], effectAllowed: "move", dropEffect: "none",
    };
    await fireEvent.dragOver(stageZone, { dataTransfer: dt });
    await fireEvent.drop(stageZone, { dataTransfer: dt });
    await new Promise(r => setTimeout(r, 30));

    // The same-session drop is rejected. There is no split assignment.
    expect(spy).not.toHaveBeenCalledWith("ws-1");
    expect(layout.splitId).toBeNull();
  });

  it("choosing a NOT-open session for the split spawns its pty (openWorkspace) and restores the active session", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha (ws-1) as active.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
    layout.setView("agent");
    layout.toggleSplit(); // Split turns on, and with splitId null, the picker shows.
    await tick();

    vi.mocked(openWorkspace).mockClear();

    // Pick Beta (ws-2), which has not been opened. Its pty must be spawned.
    const select = screen.getByRole("combobox", { name: "secondary session" }) as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: "ws-2" } });
    await tick();

    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-2"));
    expect(layout.splitId).toBe("ws-2");
    // The active, primary session stays ws-1. The split assignment did not hijack it.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveAttribute("aria-current", "page")
    );
  });
});

// ---------------------------------------------------------------------------
// codePath resets on session switch.
// ---------------------------------------------------------------------------
describe("App.svelte code-layout keep-alive across session switch (F14a)", () => {
  // Helpers: with per-session code layouts, several editors/terminals are mounted
  // at once (the hidden ones kept alive), so scope queries by session worktree/pane.
  const editorFor = (worktree: string) =>
    document.querySelector(`[data-testid="editor"][data-worktree="${worktree}"]`) as HTMLElement | null;
  const terminalFor = (paneId: string) =>
    document.querySelector(`[data-testid="terminal"][data-pane-id="${paneId}"]`) as HTMLElement | null;

  it("a file open in one session's code view does NOT leak into the next session", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha and go to code view.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();

    // Open a file in Alpha. Alpha's editor path is set.
    // Only Alpha is open, so only a single editor is mounted here.
    await fireEvent.click(screen.getByRole("button", { name: "open file" }));
    await waitFor(() => expect(editorFor("/tmp/alpha")?.dataset.path).toBe("/some/file.ts"));

    // Switch to Beta and open it. Beta gets its own editor, starting empty.
    // Alpha's path must not leak into Beta.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();

    await waitFor(() => expect(editorFor("/tmp/beta")?.dataset.path).toBe(""));
  });

  it("switching away and back preserves each session's open file and terminal node", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha, code view, open file X.
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();
    await fireEvent.click(screen.getByRole("button", { name: "open file" }));
    await waitFor(() => expect(editorFor("/tmp/alpha")?.dataset.path).toBe("/some/file.ts"));

    // Capture Alpha's live editor + terminal DOM nodes.
    const alphaEditor = editorFor("/tmp/alpha");
    const alphaTerminal = terminalFor("p1");
    expect(alphaEditor).not.toBeNull();
    expect(alphaTerminal).not.toBeNull();

    // Open Beta and switch to it (code view). Beta's own editor is empty.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();
    await waitFor(() => expect(editorFor("/tmp/beta")?.dataset.path).toBe(""));

    // Alpha's layout was hidden, not destroyed. Its file selection is retained.
    expect(editorFor("/tmp/alpha")?.dataset.path).toBe("/some/file.ts");

    // Switch back to Alpha. The same editor and terminal instances are still there, still holding file X.
    // This is keep-alive, not a fresh remount.
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
    await tick();
    await waitFor(() => expect(editorFor("/tmp/alpha")?.dataset.path).toBe("/some/file.ts"));
    expect(editorFor("/tmp/alpha")).toBe(alphaEditor);
    expect(terminalFor("p1")).toBe(alphaTerminal);
  });
});

// ---------------------------------------------------------------------------
// onSelect reopens the active-but-dead session (code/diff view path).
// ---------------------------------------------------------------------------
describe("App.svelte reopen active dead session via sidebar", () => {
  it("clicking the active session's dimmed row when its pty is dead reopens it (openWorkspace)", async () => {
    const { listWorkspaces, closeWorkspace, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Close so the session is active-but-dead, then move to the CODE view (no in-pane Reopen there).
    await fireEvent.click(screen.getByRole("menuitem", { name: "Session" }));
    await tick();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Close session" }));
    await waitFor(() => expect(closeWorkspace).toHaveBeenCalledWith("ws-1"));
    layout.setView("code");
    await tick();
    vi.mocked(openWorkspace).mockClear();

    // Clicking the dimmed active row must reopen it (not a no-op).
    await fireEvent.click(screen.getByRole("button", { name: /^Alpha\b/ }));
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));
  });
});

// ---------------------------------------------------------------------------
// Undo restores the removed session's selection.
// ---------------------------------------------------------------------------
describe("App.svelte undo restores selection", () => {
  it("undoing a remove of the ACTIVE session re-selects it", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
        repoPath: "/repo/repo-alpha",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    await waitFor(() => expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveAttribute("aria-current", "page"));

    // Remove the session through the command palette, then confirm.
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    await waitFor(() => expect(screen.getByRole("dialog", { name: "command palette" })).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("option", { name: /remove session/i }));
    await tick();
    await waitFor(() => expect(screen.getByRole("dialog", { name: "confirm" })).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await tick();

    // Active cleared while hidden.
    await waitFor(() => expect(screen.queryByRole("button", { name: /^Alpha\b/ })).not.toBeInTheDocument());

    // Undo. The row returns and is re-selected, so active is restored.
    const toast = await screen.findByTestId("undo-toast");
    await fireEvent.click(within(toast).getByRole("button", { name: "Undo" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /^Alpha\b/ })).toHaveAttribute("aria-current", "page")
    );
  });
});

// ---------------------------------------------------------------------------
// Preview keep-alive across view switch (F8a): the Preview pane is hidden on the
// agent view (visible=false, render frozen), not destroyed, so its node survives.
// ---------------------------------------------------------------------------
describe("App.svelte Preview keep-alive (F8a)", () => {
  it("code→agent→code preserves the Preview node identity", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    layout.setView("code");
    await tick();

    // Open a markdown file. The Preview renders.
    await fireEvent.click(screen.getByRole("button", { name: "open markdown" }));
    const previewBefore = await screen.findByTestId("preview");
    expect(previewBefore.dataset.path).toBe("/some/file.md");

    // Switch to agent view, which hides it, then back to code view. It is the same Preview node, with the same file.
    layout.setView("agent");
    await tick();
    layout.setView("code");
    await tick();
    await waitFor(() => expect(screen.getByTestId("preview").dataset.path).toBe("/some/file.md"));
    expect(screen.getByTestId("preview")).toBe(previewBefore);
  });
});

// ---------------------------------------------------------------------------
// F20a: the shell drawer is gated on mountedWorkspaces, so an AGENT pty exit does
// NOT unmount the session's independent shell pty.
// ---------------------------------------------------------------------------
describe("App.svelte shell drawer survives an agent pty exit (F20a)", () => {
  it("agent pty:exit leaves the session's shell drawer mounted", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([
      {
        id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
        worktreePath: "/tmp/alpha", repoPath: "/repo/repo-alpha", agent: "claude", paneId: "p1", lastActive: "",
        caps: { approvals: false, attention: false },
      },
    ]);
    const { terminalExitHandlers } = await import("./lib/__stubs__/terminalExit");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    const shellPresent = () =>
      screen.getAllByTestId("shell-drawer-probe").some(p => p.getAttribute("data-pane-id") === "shell-ws-1");
    await waitFor(() => expect(shellPresent()).toBe(true));

    // The agent pty exits. App.handleAgentExit drops ws-1 from openIds but keeps it active.
    // mountedWorkspaces still includes it, so the shell drawer stays mounted.
    await waitFor(() => expect(terminalExitHandlers["p1"]).toBeTypeOf("function"));
    terminalExitHandlers["p1"](0);
    await tick();

    // The exited-agent overlay is up, but the shell pty is untouched.
    await waitFor(() => expect(screen.getByTestId("pane-ended")).toBeInTheDocument());
    expect(shellPresent()).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// F21: openNewSession scans the filesystem for repos only ONCE.
// ---------------------------------------------------------------------------
describe("App.svelte discoverRepos runs once (F21)", () => {
  it("opening the New Session dialog twice calls discoverRepos once", async () => {
    const { listWorkspaces, discoverRepos } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]); // No sessions means the empty-state shows.
    const { default: App } = await import("./App.svelte");
    render(App);

    // Empty-state exposes the "New Session" button which calls openNewSession().
    const newBtn = await screen.findByRole("button", { name: "New Session" });
    await fireEvent.click(newBtn);
    await tick();
    // Open it a second time. The filesystem scan must be guarded, not repeated.
    await fireEvent.click(newBtn);
    await tick();

    expect(discoverRepos).toHaveBeenCalledTimes(1);
  });
});

// ---------------------------------------------------------------------------
// Batch 1 interaction / focus / keyboard / error findings
// (F16 focus-theft guard, F17 `i` focus, F18 attach modal-guard, F19 approval
//  prune on exit, F22 dormant direct-reopen, F23 `n`, F24 `?`/F1, F26a create
//  error humanize, F35 open-error surface, F54 chord indicator)
// ---------------------------------------------------------------------------
describe("App.svelte Batch-1 interaction findings", () => {
  const oneWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: false, attention: false },
    },
  ];
  const oneApprovalWs = [
    {
      id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
      worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
      repoPath: "/repo/repo-alpha",
      caps: { approvals: true, attention: false },
    },
  ];

  async function openAlpha() {
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();
    return alphaBtn;
  }

  it("pressing 'i' enters TERMINAL mode AND focuses the active pty (F17)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneWs);
    const { terminalFocusCalls } = await import("./lib/__stubs__/terminalExit");
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await openAlpha();

    const before = terminalFocusCalls["p1"] ?? 0;
    await fireEvent.keyDown(document.body, { key: "i" });
    await tick();
    expect(mode.current).toBe("terminal");
    expect(terminalFocusCalls["p1"] ?? 0).toBeGreaterThan(before);
  });

  // The awaiting-input auto-focus flips the mode to TERMINAL and focuses the pty.
  // It is suppressed while a modal is open, or while the command palette owns the keyboard (`modalOpen || mode==='command'`).
  // Settings is one member of `modalOpen`, but it cannot render under the App.test settings mock, because SettingsPanel reads several fields the mock omits.
  // So this test exercises the modal branch through the New Session dialog, and the palette branch through the command palette. The guard logic is identical for both.
  it("a background awaiting-input does NOT steal focus/mode while a modal or the palette is open (F16)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneWs);
    const { layout } = await import("./lib/stores/layout.svelte");
    const { mode } = await import("./lib/stores/mode.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);
    await openAlpha();
    layout.setView("agent");
    const cb = captured.agent.at(-1)!;

    // --- modalOpen branch: open the New Session dialog (a modal) ---
    await fireEvent.keyDown(document.body, { key: "n" });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );
    expect(mode.current).toBe("normal");
    // Alpha (active, agent view) newly asks for input while the modal is open.
    cb({ workspaceId: "ws-1", kind: "question", state: "awaiting-input" });
    await tick();
    // Focus and mode theft are suppressed. The keyboard stays with the modal.
    expect(mode.current).not.toBe("terminal");

    // Close the dialog and reset the edge, from running back to awaiting-input.
    await fireEvent.keyDown(document.body, { key: "Escape" });
    await tick();
    cb({ workspaceId: "ws-1", kind: "state", state: "running" });
    await tick();

    // --- command branch: open the command palette (mode='command') ---
    await fireEvent.keyDown(document.body, { key: ":" });
    await tick();
    expect(mode.current).toBe("command");
    cb({ workspaceId: "ws-1", kind: "question", state: "awaiting-input" });
    await tick();
    // The mode is still "command". The palette keeps the keyboard, not the pty.
    expect(mode.current).toBe("command");
  });

  it("a background attach does NOT swap an open resume-preview (F18)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Alpha's resume-preview (do NOT confirm).
    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    expect(screen.getByText(/Resume: Alpha/)).toBeInTheDocument();

    // A background attach for beta arrives while the preview modal is open.
    const cb = captured.workspaceAttach.at(-1)!;
    cb({ query: "beta" });
    await tick();

    // The preview still targets Alpha. The attach did not hijack it.
    expect(screen.getByText(/Resume: Alpha/)).toBeInTheDocument();
    expect(screen.queryByText(/Resume: Beta/)).not.toBeInTheDocument();
  });

  it("an agent pty exit prunes the dead session's pending approval card (F19)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneApprovalWs);
    const { terminalExitHandlers } = await import("./lib/__stubs__/terminalExit");
    const { default: App } = await import("./App.svelte");
    render(App);
    await openAlpha();

    // Seed a pending approval for the active session. The card docks.
    const cb = captured.agent.at(-1)!;
    cb({ workspaceId: "ws-1", kind: "approval", state: "awaiting-approval",
         approval: { reqId: "req-x", tool: "bash", summary: "do a thing" } });
    await tick();
    await waitFor(() => expect(screen.getByLabelText("approval card")).toBeInTheDocument());

    // The agent pty exits. Its pending approval must be pruned, or the card lingers, pointing at a reqId whose agent is gone.
    // Without the prune, the card would be undismissable.
    await waitFor(() => expect(terminalExitHandlers["p1"]).toBeTypeOf("function"));
    terminalExitHandlers["p1"](0);
    await tick();
    expect(screen.queryByLabelText("approval card")).not.toBeInTheDocument();
  });

  it("a dormant background row reopens DIRECTLY with no resume-preview (F22)", async () => {
    const { listWorkspaces, openWorkspace, closeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(fakeWorkspaces);
    const { default: App } = await import("./App.svelte");
    render(App);

    // Open Beta. It is cold, so the flow goes preview then open. This makes it "opened this run".
    const betaBtn = await screen.findByRole("button", { name: /^Beta\b/ });
    await fireEvent.click(betaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    // Close Beta, which is active. It is now dormant: kept, but with no live pty.
    await fireEvent.click(screen.getByRole("menuitem", { name: "Session" }));
    await tick();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Close session" }));
    await waitFor(() => expect(closeWorkspace).toHaveBeenCalledWith("ws-2"));

    // Open Alpha. It is cold, so the flow goes preview then open. This makes Beta a background dormant row.
    const alphaBtn = screen.getByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    vi.mocked(openWorkspace).mockClear();

    // Click Beta's dormant background row. This directly reopens it, with no resume-preview.
    await fireEvent.click(screen.getByRole("button", { name: /^Beta\b/ }));
    await tick();
    expect(screen.queryByTestId("resume-preview")).not.toBeInTheDocument();
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-2"));
  });

  it("pressing 'n' in NORMAL opens the New Session dialog (F23)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    await fireEvent.keyDown(document.body, { key: "n" });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument()
    );
  });

  it("pressing 'x' on the active session opens the cancelable remove confirm (F23)", async () => {
    const { listWorkspaces, removeWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneWs);
    const { default: App } = await import("./App.svelte");
    render(App);
    await openAlpha();

    // A lone `x` must not act immediately. It only opens the confirm dialog.
    expect(screen.queryByRole("dialog", { name: "confirm" })).not.toBeInTheDocument();
    await fireEvent.keyDown(document.body, { key: "x" });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "confirm" })).toBeInTheDocument()
    );
    // Nothing is removed yet. The guard is the confirm click, not the keypress.
    expect(removeWorkspace).not.toHaveBeenCalled();
  });

  it("pressing '?' and F1 in NORMAL open the Help dialog (F24)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    await fireEvent.keyDown(document.body, { key: "?" });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "help" })).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByRole("button", { name: "close help" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "help" })).not.toBeInTheDocument()
    );

    await fireEvent.keyDown(document.body, { key: "F1" });
    await waitFor(() =>
      expect(screen.getByRole("dialog", { name: "help" })).toBeInTheDocument()
    );
  });

  it("a branch-exists create error is humanized inline in the dialog, never raw git (F26a)", async () => {
    const { listWorkspaces, createWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneWs);
    vi.mocked(createWorkspace).mockRejectedValueOnce(
      new Error("git worktree add: fatal: a branch named 'claude/work' already exists")
    );
    const { default: App } = await import("./App.svelte");
    render(App);
    await screen.findByRole("button", { name: /^Alpha\b/ });

    await fireEvent.click(screen.getByRole("button", { name: "New session" }));
    await waitFor(() => screen.getByRole("dialog", { name: "new session" }));
    await waitFor(() => {
      const sp = screen.getByLabelText(/starting point/i) as HTMLSelectElement;
      expect(sp.options.length).toBeGreaterThan(0);
    });

    await fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await tick();

    // This is a humanized, actionable inline error, not the raw git plumbing.
    await waitFor(() => {
      const dlg = screen.getByRole("dialog", { name: "new session" });
      expect(within(dlg).getByRole("alert").textContent).toMatch(/already exists/i);
    });
    expect(screen.queryByText(/fatal:/)).not.toBeInTheDocument();
    // Dialog stays open so the user can correct the branch name.
    expect(screen.getByRole("dialog", { name: "new session" })).toBeInTheDocument();
  });

  it("a failed OpenWorkspace surfaces a blocking notification (F35)", async () => {
    const { listWorkspaces, openWorkspace } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(oneWs);
    vi.mocked(openWorkspace).mockRejectedValueOnce(new Error("spawn pty: no such file or directory"));
    const { getItems } = await import("./lib/stores/notifications.svelte");
    const { default: App } = await import("./App.svelte");
    render(App);

    const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
    await fireEvent.click(alphaBtn);
    await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
    await tick();

    await waitFor(() =>
      expect(getItems().some((n) => n.title === "Could not open session")).toBe(true)
    );
  });

  it("pressing 'g' shows a transient chord indicator that auto-clears (F54)", async () => {
    const { listWorkspaces } = await import("./lib/wails");
    (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { default: App } = await import("./App.svelte");
    render(App);
    await tick();

    vi.useFakeTimers();
    try {
      await fireEvent.keyDown(document.body, { key: "g" });
      await tick();
      expect(screen.getByTestId("pending-chord")).toBeInTheDocument();

      // Times out and clears (auto-clear ~1.5s; advance well past it).
      vi.advanceTimersByTime(2000);
      await tick();
      expect(screen.queryByTestId("pending-chord")).not.toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});

// ---------------------------------------------------------------------------
// Audit regressions (FEC / FEX). Each test pins one defect found in the
// frontend audit.
// ---------------------------------------------------------------------------
describe("audit regressions: App wiring", () => {
  it("FEC-8: the persisted Glass setting reaches ThemeProvider (data-glass)", async () => {
    const { settings } = await import("./lib/stores/settings.svelte");
    (settings as any).glass = false;
    try {
      const { default: App } = await import("./App.svelte");
      render(App);
      await tick();
      expect(document.documentElement.getAttribute("data-glass")).toBe("off");
    } finally {
      delete (settings as any).glass;
    }
  });
});
