// frontend/src/App.smoke.test.ts
// 4.25.7 — Full-composition smoke test
// One end-to-end scenario that exercises the wired path: assembly → workspace select →
// view switching → approval flow → notification hub.

import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { vi, it, expect, beforeEach } from "vitest";

// ---------------------------------------------------------------------------
// Stubs — mirror App.test.ts exactly
// ---------------------------------------------------------------------------

// Stub ShellDrawer (imports xterm which crashes jsdom).
vi.mock("./lib/ShellDrawer.svelte", async () => ({
  default: (await import("./lib/__stubs__/Empty.svelte")).default,
}));

// Stub heavy children — xterm/CodeMirror crash jsdom.
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

// Captured callbacks — reset in beforeEach.
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
}));

// Mirror App.test.ts: mock settings (real settings.load() is unverified in jsdom).
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
// Fake workspaces — ≥2 with caps.approvals:true and caps.tokens:true
// ---------------------------------------------------------------------------
const smokeWorkspaces = [
  {
    id: "ws-1", title: "Alpha", branch: "main", state: "idle" as const,
    worktreePath: "/tmp/alpha", agent: "claude", paneId: "p1", lastActive: "",
    caps: { approvals: true, attention: false, tokens: true },
  },
  {
    id: "ws-2", title: "Beta", branch: "feat/beta", state: "running" as const,
    worktreePath: "/tmp/beta", agent: "claude", paneId: "p2", lastActive: "",
    caps: { approvals: true, attention: false, tokens: true },
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

  // Ensure DND is off so ambient notifications are not filtered.
  const { setDnd } = await import("./lib/stores/notifications.svelte");
  setDnd(false);
});

// ---------------------------------------------------------------------------
// THE SMOKE TEST
// ---------------------------------------------------------------------------
it("4.25.7 full-composition smoke: assembly → select → view-switch → approval → notification", async () => {
  const { listWorkspaces, openWorkspace, approve } = await import("./lib/wails");
  (listWorkspaces as ReturnType<typeof vi.fn>).mockResolvedValue(smokeWorkspaces);

  const { layout } = await import("./lib/stores/layout.svelte");
  const { default: App } = await import("./App.svelte");

  // -------------------------------------------------------------------------
  // Step 1 — Assembly: core chrome mounts
  // -------------------------------------------------------------------------
  render(App);

  // MenuBar role="menubar"
  expect(document.querySelector('[role="menubar"]')).toBeInTheDocument();

  // Sidebar: wait for workspaces to load, then check both buttons
  const alphaBtn = await screen.findByRole("button", { name: "Alpha" });
  expect(alphaBtn).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Beta" })).toBeInTheDocument();

  // Stage view-switcher nav (aria-label="View")
  expect(document.querySelector('nav[aria-label="View"]')).toBeInTheDocument();

  // Empty-state is shown before any workspace is selected
  expect(document.querySelector(".empty-state")).toBeInTheDocument();

  // -------------------------------------------------------------------------
  // Step 2 — Select workspace → openWorkspace called, Terminal probe mounts
  // -------------------------------------------------------------------------
  await fireEvent.click(alphaBtn);
  await tick();

  expect(openWorkspace).toHaveBeenCalledWith("ws-1");

  const terminal = await screen.findByTestId("terminal");
  expect(terminal).toBeInTheDocument();
  expect(terminal.dataset.paneId).toBe("p1");

  // Now that active is set, TokenMeter should be visible (caps.tokens=true)
  await waitFor(() =>
    expect(screen.getByRole("status", { name: "token usage" })).toBeInTheDocument()
  );

  // -------------------------------------------------------------------------
  // Step 3 — View switching via keymap (REAL keymap + REAL layout store)
  // -------------------------------------------------------------------------

  // '2' → Code view → EditorProbe mounts
  await fireEvent.keyDown(document.body, { key: "2" });
  await tick();
  await waitFor(() => {
    expect(screen.queryByTestId("terminal")).not.toBeInTheDocument();
    expect(screen.getByTestId("editor")).toBeInTheDocument();
  });

  // '3' → Diff view → DiffProbe mounts
  await fireEvent.keyDown(document.body, { key: "3" });
  await tick();
  await waitFor(() => {
    expect(screen.queryByTestId("editor")).not.toBeInTheDocument();
    expect(screen.getByTestId("diff")).toBeInTheDocument();
    expect(screen.getByTestId("diff").dataset.worktree).toBe("/tmp/alpha");
  });

  // '1' → Agent view → Terminal probe is back
  await fireEvent.keyDown(document.body, { key: "1" });
  await tick();
  await waitFor(() => {
    expect(screen.queryByTestId("diff")).not.toBeInTheDocument();
    expect(screen.getByTestId("terminal")).toBeInTheDocument();
  });

  // -------------------------------------------------------------------------
  // Step 4 — Approval flow: event → card surfaces → Allow → approve() called → card gone
  // -------------------------------------------------------------------------

  // There is always exactly one registered agent-event callback (the one App registered in onMount).
  const agentCb = captured.agent.at(-1)!;
  agentCb({
    workspaceId: "ws-1",
    kind: "approval",
    state: "awaiting-approval",
    approval: { reqId: "r1", tool: "bash", summary: "rm -rf" },
  });
  await tick();

  // ApprovalCard must surface with the summary text
  await waitFor(() =>
    expect(screen.getByText("rm -rf")).toBeInTheDocument()
  );
  expect(screen.getByRole("button", { name: "Allow" })).toBeInTheDocument();

  // Click Allow
  await fireEvent.click(screen.getByRole("button", { name: "Allow" }));
  await tick();

  expect(approve).toHaveBeenCalledWith("r1", "allow");

  // Card must disappear after dequeue
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument()
  );
  await waitFor(() =>
    expect(screen.queryByText("rm -rf")).not.toBeInTheDocument()
  );

  // -------------------------------------------------------------------------
  // Step 5 — Notifications: inject ambient → open hub via bell → entry visible
  // -------------------------------------------------------------------------

  const notifyCb = captured.notify.at(-1)!;
  notifyCb({
    tier: "ambient",
    title: "Turn done",
    body: "Agent completed a turn",
    workspaceId: "ws-1",
  });
  await tick();

  // Open the notification hub via the MenuBar bell
  const bellBtn = screen.getByRole("button", { name: "notifications" });
  await fireEvent.click(bellBtn);
  await tick();

  // Hub must now be visible
  expect(screen.getByRole("region", { name: "notification hub" })).toBeInTheDocument();

  // The notification title must appear in the hub
  await waitFor(() =>
    expect(screen.getByText("Turn done")).toBeInTheDocument()
  );
});
