// frontend/src/App.smoke.test.ts
// Full-composition smoke test
// One end-to-end scenario tests the full wired path: assembly, workspace
// selection, view switching, approval flow, and notification hub.

import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { vi, it, expect, beforeEach } from "vitest";

// ---------------------------------------------------------------------------
// Stubs. These match App.test.ts exactly.
// ---------------------------------------------------------------------------

// Stub ShellDrawer. It imports xterm, and xterm crashes jsdom.
vi.mock("./lib/ShellDrawer.svelte", async () => ({
  default: (await import("./lib/__stubs__/Empty.svelte")).default,
}));

// Stub heavy child components. xterm and CodeMirror crash jsdom.
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

// Captured callbacks. beforeEach resets these.
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
  approve:         vi.fn(async () => {}),
  createWorkspace: vi.fn(async (_agent: string, _repoPath: string, _baseRef: string, _branch: string, _title: string, _worktree: boolean) => ({
    id: "ws-new", title: "New", branch: "main", state: "idle",
    worktreePath: "/tmp/new", repoPath: "/repo/New", agent: "claude", paneId: "p-new", lastActive: "",
    caps: { approvals: false, attention: false },
  })),
  setWorkspaceTitle: vi.fn(async (_id: string, _title: string) => {}),
  removeWorkspace: vi.fn(async () => {}),
  writeToPty:      vi.fn(async () => {}),
  closeShell:      vi.fn(async () => {}),
  reloadAgentEnv:  vi.fn(async () => {}),
  branches:        vi.fn(async (_repo: string) => ["main", "feat/x"]),
  onAgentEvent:    vi.fn((cb) => { captured.agent.push(cb);     return () => {}; }),
  onNotify:        vi.fn((cb) => { captured.notify.push(cb);    return () => {}; }),
  onFsChanged:     vi.fn((cb) => { captured.fsChanged.push(cb); return () => {}; }),
  onWorkspaceAttach: vi.fn(() => () => {}),
  onWorkspaceRelaunch: vi.fn(() => () => {}),
  diffStat:        vi.fn(async () => []),
  setWindowFocus:  vi.fn(async () => {}),
}));

// Match App.test.ts: mock the settings store. Real settings.load() is unverified in jsdom.
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

// ---------------------------------------------------------------------------
// Fake workspaces: at least 2, each with caps.approvals:true.
// ---------------------------------------------------------------------------
const smokeWorkspaces = [
  {
    id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
    worktreePath: "/tmp/alpha", repoPath: "/repo/Alpha", agent: "claude", paneId: "p1", lastActive: "",
    caps: { approvals: true, attention: false },
  },
  {
    id: "ws-2", title: "Beta", branch: "feat/beta", state: "running" as const,
    worktreePath: "/tmp/beta", repoPath: "/repo/Beta", agent: "claude", paneId: "p2", lastActive: "",
    caps: { approvals: true, attention: false },
  },
];

// ---------------------------------------------------------------------------
// Reset shared state before each run
// ---------------------------------------------------------------------------
beforeEach(async () => {
  vi.clearAllMocks();
  captured.agent.length     = 0;
  captured.notify.length    = 0;
  captured.fsChanged.length = 0;

  const { layout } = await import("./lib/stores/layout.svelte");
  layout.setView("agent");
  layout.split = false as any;
  (layout as any).sidebarW  = 240;
  (layout as any).shellH    = 200;
  (layout as any).collapsed = {};

  const { mode } = await import("./lib/stores/mode.svelte");
  mode.leaveCommand();
  (mode as any).current = "normal";

  // Turn off DND so the app does not filter ambient notifications.
  const { setDnd } = await import("./lib/stores/notifications.svelte");
  setDnd(false);
});

// ---------------------------------------------------------------------------
// THE SMOKE TEST
// ---------------------------------------------------------------------------
it("full-composition smoke: assembly → select → view-switch → approval → notification", async () => {
  const { listWorkspaces, openWorkspace, approve } = await import("./lib/wails");
  (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(smokeWorkspaces);

  const { layout } = await import("./lib/stores/layout.svelte");
  const { default: App } = await import("./App.svelte");

  // -------------------------------------------------------------------------
  // Step 1: assembly. Core chrome mounts.
  // -------------------------------------------------------------------------
  render(App);

  // MenuBar role="menubar"
  expect(document.querySelector('[role="menubar"]')).toBeInTheDocument();

  // Sidebar: wait for the workspaces to load. Then check both buttons.
  const alphaBtn = await screen.findByRole("button", { name: /^Alpha\b/ });
  expect(alphaBtn).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /^Beta\b/ })).toBeInTheDocument();

  // Check the view-switcher nav (aria-label="View").
  expect(document.querySelector('nav[aria-label="View"]')).toBeInTheDocument();

  // The empty state appears before the user selects a workspace.
  expect(document.querySelector(".empty-state")).toBeInTheDocument();

  // -------------------------------------------------------------------------
  // Step 2: select a workspace. The resume preview appears. The user confirms.
  // openWorkspace runs, and the Terminal probe mounts.
  // -------------------------------------------------------------------------
  await fireEvent.click(alphaBtn);
  // The resume preview modal appears. Confirm it to open the session.
  await waitFor(() => expect(screen.getByTestId("resume-preview")).toBeInTheDocument());
  expect(openWorkspace).not.toHaveBeenCalled();
  await fireEvent.click(screen.getByRole("button", { name: /^open$/i }));
  await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith("ws-1"));

  const terminal = await screen.findByTestId("terminal");
  expect(terminal).toBeInTheDocument();
  expect(terminal.dataset.paneId).toBe("p1");

  // -------------------------------------------------------------------------
  // Step 3: view switching by keymap. This test uses the real keymap and the real layout store.
  // -------------------------------------------------------------------------

  // Key '2' opens the Code view. The editor becomes visible. The terminal stays
  // mounted but hidden, so its buffer survives.
  await fireEvent.keyDown(document.body, { key: "2" });
  await tick();
  await waitFor(() => {
    expect(screen.getByTestId("editor")).toBeVisible();
    expect(screen.getByTestId("terminal")).not.toBeVisible();
  });

  // Key '3' opens the Diff view. The diff becomes visible. The editor stays
  // mounted but hidden.
  await fireEvent.keyDown(document.body, { key: "3" });
  await tick();
  await waitFor(() => {
    expect(screen.getByTestId("diff")).toBeVisible();
    expect(screen.getByTestId("diff").dataset.worktree).toBe("/tmp/alpha");
    expect(screen.getByTestId("editor")).not.toBeVisible();
  });

  // Key '1' returns to the Agent view. The terminal becomes visible again.
  // The DiffView stays mounted but hidden, so its expanded hunks and scroll
  // position survive a view switch (F3). The app does not destroy and
  // refetch the DiffView every time.
  await fireEvent.keyDown(document.body, { key: "1" });
  await tick();
  await waitFor(() => {
    expect(screen.getByTestId("terminal")).toBeVisible();
  });
  expect(screen.getByTestId("diff")).not.toBeVisible();

  // -------------------------------------------------------------------------
  // Step 4: approval flow. An event triggers the approval card. The user
  // clicks Allow. approve() runs, and the card disappears.
  // -------------------------------------------------------------------------

  // App registers exactly one agent-event callback, in onMount.
  const agentCb = captured.agent.at(-1)!;
  agentCb({
    workspaceId: "ws-1",
    kind: "approval",
    state: "awaiting-approval",
    approval: { reqId: "r1", tool: "bash", summary: "rm -rf" },
  });
  await tick();

  // The ApprovalCard must show the summary text.
  await waitFor(() =>
    expect(screen.getByText("rm -rf")).toBeInTheDocument()
  );
  expect(screen.getByRole("button", { name: "Allow" })).toBeInTheDocument();

  // Click Allow
  await fireEvent.click(screen.getByRole("button", { name: "Allow" }));
  await tick();

  expect(approve).toHaveBeenCalledWith("r1", "allow");

  // The card must disappear after the approval leaves the queue.
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument()
  );
  await waitFor(() =>
    expect(screen.queryByText("rm -rf")).not.toBeInTheDocument()
  );

  // -------------------------------------------------------------------------
  // Step 5: notifications. Inject an ambient notification. Open the
  // notification hub with the bell. The entry becomes visible.
  // -------------------------------------------------------------------------

  const notifyCb = captured.notify.at(-1)!;
  notifyCb({
    tier: "ambient",
    title: "Turn done",
    body: "Agent completed a turn",
    workspaceId: "ws-1",
  });
  await tick();

  // Open the notification hub with the MenuBar bell.
  const bellBtn = screen.getByRole("menuitem", { name: "notifications" });
  await fireEvent.click(bellBtn);
  await tick();

  // The notification hub must now be visible.
  expect(screen.getByRole("region", { name: "notification hub" })).toBeInTheDocument();

  // The notification title must appear in the hub
  await waitFor(() =>
    expect(screen.getByText("Turn done")).toBeInTheDocument()
  );
});
