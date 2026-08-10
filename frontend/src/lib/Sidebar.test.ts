// frontend/src/lib/Sidebar.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";
import type { WorkspaceVM } from "./wails";

// Each fake sets title equal to the basename of repoPath — the row's primary
// label is now the user-chosen ws.title, so selectors by that name still resolve
// while the repo name is shown as a dim secondary span.
// Titles are the row's bold primary label; repo basenames are kept distinct from
// the titles so getByText(title) resolves to exactly one element (the title span),
// while the repo name still renders as a dim secondary span.
const workspaces: WorkspaceVM[] = [
  { id: "ws_a", worktreePath: "/wt/a", repoPath: "/repo/repo-auth", agent: "claude", title: "feat-auth",
    branch: "feat/auth", state: "running", caps: { approvals: false, attention: false }, paneId: "p1", lastActive: "" },
  { id: "ws_b", worktreePath: "/wt/b", repoPath: "/repo/repo-core", agent: "claude", title: "feat-core",
    branch: "feat/core", state: "idle", caps: { approvals: false, attention: false }, paneId: "p2", lastActive: "" },
  { id: "ws_c", worktreePath: "/wt/c", repoPath: "/repo/repo-bug", agent: "claude", title: "bug-fix",
    branch: "fix/crash", state: "awaiting-approval", caps: { approvals: true, attention: false }, paneId: "p3", lastActive: "" },
  { id: "ws_d", worktreePath: "/wt/d", repoPath: "/repo/repo-done", agent: "claude", title: "done-work",
    branch: "feat/done", state: "done", caps: { approvals: false, attention: false }, paneId: "p4", lastActive: "" },
  { id: "ws_e", worktreePath: "/wt/e", repoPath: "/repo/repo-errored", agent: "claude", title: "errored-work",
    branch: "feat/err", state: "errored", caps: { approvals: false, attention: false }, paneId: "p5", lastActive: "" },
  { id: "ws_q", worktreePath: "/wt/q", repoPath: "/repo/repo-asking", agent: "claude", title: "asking-work",
    branch: "feat/ask", state: "awaiting-input", caps: { approvals: false, attention: true }, paneId: "p6", lastActive: "" },
];

test("renders status icon+label for all states", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  expect(screen.getByText(/◐/)).toBeInTheDocument();
  expect(screen.getByText(/running/i)).toBeInTheDocument();
  expect(screen.getByText(/◯/)).toBeInTheDocument();
  expect(screen.getByText(/idle/i)).toBeInTheDocument();
  expect(screen.getByText(/⚠/)).toBeInTheDocument();
  expect(screen.getByText(/needs you/i)).toBeInTheDocument();
  expect(screen.getByText(/✓/)).toBeInTheDocument();
  expect(screen.getByText(/✗/)).toBeInTheDocument();
});

test("awaiting-input maps to the question feel: status-awaiting-input class + 'asking you' label", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });

  const askBtn = screen.getByRole("button", { name: /asking-work/ });
  const icon = askBtn.querySelector(".status-icon")!;
  expect(icon).toBeInTheDocument();
  expect(icon.classList.contains("status-awaiting-input")).toBe(true);
  expect(icon.textContent).toContain("?");
  expect(askBtn).toHaveTextContent("asking you");
});

test("done state renders the ✓ icon with status-done class", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });

  const doneBtn = screen.getByRole("button", { name: /done-work/ });
  const icon = doneBtn.querySelector(".status-icon")!;
  expect(icon.classList.contains("status-done")).toBe(true);
  expect(icon.textContent).toContain("✓");
});

test("clicking a workspace calls onSelect", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onSelect = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-core"));
  await fireEvent.click(screen.getByRole("button", { name: /feat-core/ }));
  expect(onSelect).toHaveBeenCalledWith("ws_b");
});

test("New session button calls onNew", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onNew = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew } });
  await fireEvent.click(screen.getByRole("button", { name: /new session/i }));
  expect(onNew).toHaveBeenCalled();
});

// --- Behavior 4a+4b: session drag-to-reorder ---

test("dragstart on a session row sets application/x-perch-session to the workspace id", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-auth"));

  // Get the <li> that wraps feat-auth (draggable element)
  const btn = screen.getByRole("button", { name: /feat-auth/ });
  const li  = btn.closest("li") as HTMLElement;
  expect(li).toBeTruthy();

  const store = new Map<string, string>();
  const dt = {
    setData: vi.fn((type: string, value: string) => { store.set(type, value); }),
    getData: (type: string) => store.get(type) ?? "",
    effectAllowed: "uninitialized" as string,
    files: [],
    types: [] as string[],
  };
  await fireEvent.dragStart(li, { dataTransfer: dt });
  expect(dt.setData).toHaveBeenCalledWith("application/x-perch-session", "ws_a");
  expect(dt.effectAllowed).toBe("move");
});

test("dropping session A onto session B's row calls onReorder(A, B)", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onReorder = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, onReorder } });
  await waitFor(() => screen.getByText("feat-core"));

  const targetBtn = screen.getByRole("button", { name: /feat-core/ });
  const targetLi  = targetBtn.closest("li") as HTMLElement;

  // Simulate a session drop carrying ws_a onto ws_b's row
  const store = new Map<string, string>([["application/x-perch-session", "ws_a"]]);
  const dt = {
    setData: vi.fn(),
    getData: (type: string) => store.get(type) ?? "",
    types: ["application/x-perch-session"],
    files: [],
    effectAllowed: "move" as string,
    dropEffect: "none" as string,
  };
  await fireEvent.dragOver(targetLi, { dataTransfer: dt });
  await fireEvent.drop(targetLi, { dataTransfer: dt });
  expect(onReorder).toHaveBeenCalledWith("ws_a", "ws_b");
});

test("dropping a session onto its own row does NOT call onReorder", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onReorder = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, onReorder } });
  await waitFor(() => screen.getByText("feat-auth"));

  const selfBtn = screen.getByRole("button", { name: /feat-auth/ });
  const selfLi  = selfBtn.closest("li") as HTMLElement;

  const store = new Map<string, string>([["application/x-perch-session", "ws_a"]]);
  const dt = {
    setData: vi.fn(),
    getData: (type: string) => store.get(type) ?? "",
    types: ["application/x-perch-session"],
    files: [],
    effectAllowed: "move" as string,
    dropEffect: "none" as string,
  };
  await fireEvent.drop(selfLi, { dataTransfer: dt });
  expect(onReorder).not.toHaveBeenCalled();
});

// ---------------------------------------------------------------------------
// diffStats prop: +/− render in sidebar rows
// ---------------------------------------------------------------------------

test("diffStats prop: row with nonzero added/removed shows .sidebar-diffstat with +N and −N", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const diffStats = {
    ws_a: { added: 5, removed: 2 },
  };
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, diffStats } });
  await waitFor(() => screen.getByText("feat-auth"));

  const featAuthBtn = screen.getByRole("button", { name: /feat-auth/ });
  const diffstatSpan = featAuthBtn.querySelector(".sidebar-diffstat");
  expect(diffstatSpan).toBeInTheDocument();
  expect(diffstatSpan!.querySelector(".diff-added")!.textContent).toContain("+5");
  expect(diffstatSpan!.querySelector(".diff-removed")!.textContent).toContain("2");
});

test("diffStats prop: row with added=0 removed=0 does NOT render .sidebar-diffstat", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const diffStats = {
    ws_a: { added: 0, removed: 0 },
  };
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, diffStats } });
  await waitFor(() => screen.getByText("feat-auth"));

  const featAuthBtn = screen.getByRole("button", { name: /feat-auth/ });
  expect(featAuthBtn.querySelector(".sidebar-diffstat")).toBeNull();
});

test("diffStats prop: row without a diffStats entry does NOT render .sidebar-diffstat", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  // ws_b has no entry in diffStats
  const diffStats = { ws_a: { added: 3, removed: 1 } };
  render(Sidebar, { props: { workspaces, activeId: "ws_b", onSelect: () => {}, onNew: () => {}, diffStats } });
  await waitFor(() => screen.getByText("feat-core"));

  const featCoreBtn = screen.getByRole("button", { name: /feat-core/ });
  expect(featCoreBtn.querySelector(".sidebar-diffstat")).toBeNull();
});

// ---------------------------------------------------------------------------
// count-up: final values in diffstat
// ---------------------------------------------------------------------------

test("diffStats countUp: diff-added and diff-removed inner spans show final numeric values on mount", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const diffStats = { ws_a: { added: 12, removed: 4 } };
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, diffStats } });
  await waitFor(() => screen.getByText("feat-auth"));

  const btn = screen.getByRole("button", { name: /feat-auth/ });
  const diffstatSpan = btn.querySelector(".sidebar-diffstat")!;
  const addedInner = diffstatSpan.querySelector(".diff-added span")!;
  const removedInner = diffstatSpan.querySelector(".diff-removed span")!;
  expect(addedInner.textContent).toBe("12");
  expect(removedInner.textContent).toBe("4");
});

// ---------------------------------------------------------------------------
// review pill: files count
// ---------------------------------------------------------------------------

test("review pill renders when files > 0 and shows the file count", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const diffStats = { ws_a: { added: 5, removed: 2, files: 3 } };
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, diffStats } });
  await waitFor(() => screen.getByText("feat-auth"));

  const btn = screen.getByRole("button", { name: /feat-auth/ });
  const pill = btn.querySelector(".review-pill");
  expect(pill).toBeInTheDocument();
  expect(pill!.querySelector("span")!.textContent).toBe("3");
  expect(pill!.getAttribute("aria-label")).toBe("3 files to review");
});

test("review pill is absent when files is 0", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const diffStats = { ws_a: { added: 5, removed: 2, files: 0 } };
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, diffStats } });
  await waitFor(() => screen.getByText("feat-auth"));

  const btn = screen.getByRole("button", { name: /feat-auth/ });
  expect(btn.querySelector(".review-pill")).toBeNull();
});

test("review pill is absent when files is undefined", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const diffStats = { ws_a: { added: 5, removed: 2 } };
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, diffStats } });
  await waitFor(() => screen.getByText("feat-auth"));

  const btn = screen.getByRole("button", { name: /feat-auth/ });
  expect(btn.querySelector(".review-pill")).toBeNull();
});

// ---------------------------------------------------------------------------
// Feature: per-worktree row color identity
// ---------------------------------------------------------------------------

test("workspace-row carries --row-color style for its row position", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const { worktreeColor } = await import("./constants");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-auth"));

  const btn = screen.getByRole("button", { name: /feat-auth/ }) as HTMLElement;
  // ws_a is the first row (index 0) → position-based palette entry 0 (F39 wired:
  // App now passes the loop index so the first 8 rows are pairwise distinct).
  const expected = worktreeColor("ws_a", 0);
  // style:--row-color is set as a CSS custom property on the element's inline style
  const styleAttr = btn.getAttribute("style") ?? "";
  expect(styleAttr).toContain(expected);
});

test("workspace-row --row-color is assigned by row position (F39 distinct colors)", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const { worktreeColor } = await import("./constants");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-core"));

  const btnA = screen.getByRole("button", { name: /feat-auth/ }) as HTMLElement;
  const btnB = screen.getByRole("button", { name: /feat-core/ }) as HTMLElement;
  // Rows 0 and 1 → distinct palette entries 0 and 1 (round-robin), not a hash.
  expect(btnA.getAttribute("style") ?? "").toContain(worktreeColor("ws_a", 0));
  expect(btnB.getAttribute("style") ?? "").toContain(worktreeColor("ws_b", 1));
});

test("the first WORKTREE_COLORS.length rows each render a distinct --row-color (F39 wired)", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const { WORKTREE_COLORS } = await import("./constants");
  const many: WorkspaceVM[] = Array.from({ length: WORKTREE_COLORS.length }, (_, i) => ({
    id: `wsN_${i}`, worktreePath: `/wt/n${i}`, repoPath: `/repo/repo-n${i}`, agent: "claude",
    title: `n-title-${i}`, branch: `feat/n${i}`, state: "idle" as const,
    caps: { approvals: false, attention: false }, paneId: `pn${i}`, lastActive: "",
  }));
  render(Sidebar, { props: { workspaces: many, activeId: null, onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("n-title-0"));

  const rows = Array.from(document.querySelectorAll<HTMLElement>(".workspace-row"));
  expect(rows.length).toBe(WORKTREE_COLORS.length);
  // Each row i renders palette entry i (position-based round-robin), so the
  // first 8 sessions are pairwise distinct — the live-component proof of F39,
  // not just the helper's unit test.
  rows.forEach((r, i) => {
    expect(r.getAttribute("style") ?? "").toContain(WORKTREE_COLORS[i]);
  });
  const colors = rows.map(
    (r) => (r.getAttribute("style") ?? "").match(/--row-color:\s*([^;]+)/)?.[1]?.trim(),
  );
  expect(new Set(colors).size).toBe(WORKTREE_COLORS.length);
});

// ---------------------------------------------------------------------------
// Feature: agent name + relative last-active age in session rows; empty hint
// ---------------------------------------------------------------------------

test("row renders repo · branch · agent · relative last-active", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const recentIso = new Date(Date.now() - 2 * 86400000).toISOString();
  const ws: WorkspaceVM[] = [{
    id: "ws-r", worktreePath: "/wt/r", repoPath: "/home/me/my-repo", agent: "opencode", title: "feat-r",
    branch: "feat/resume", state: "idle", caps: { approvals: false, attention: false },
    paneId: "pr", lastActive: recentIso,
  }];
  render(Sidebar, { props: { workspaces: ws, activeId: null, onSelect: () => {}, onNew: () => {} } });
  // Repo name (basename of repoPath) is shown as a dim secondary span.
  expect(screen.getByText("my-repo")).toBeInTheDocument();
  expect(screen.getByText(/feat\/resume/)).toBeInTheDocument();
  expect(screen.getByText(/opencode/i)).toBeInTheDocument();
  expect(screen.getByText(/2d ago/i)).toBeInTheDocument();
});

test("row primary label is the user title; repo name shown as secondary context", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const ws: WorkspaceVM[] = [{
    id: "ws-x", worktreePath: "/wt/x", repoPath: "/home/me/perch", agent: "claude", title: "My Session",
    branch: "claude/work", state: "idle", caps: { approvals: false, attention: false },
    paneId: "px", lastActive: "",
  }];
  render(Sidebar, { props: { workspaces: ws, activeId: null, onSelect: () => {}, onNew: () => {} } });
  // The bold primary label shows the user-chosen title.
  const titleSpan = document.querySelector(".workspace-title")!;
  expect(titleSpan.textContent).toBe("My Session");
  // The repo basename is still visible as a dim secondary span.
  const repoSpan = document.querySelector(".workspace-repo")!;
  expect(repoSpan.textContent).toBe("perch");
  // aria-label correlates title + repo + branch.
  const btn = screen.getByRole("button", { name: "My Session perch claude/work" });
  expect(btn).toBeInTheDocument();
});

test("row falls back to ws.title as primary label when repoPath is empty", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const ws: WorkspaceVM[] = [{
    id: "ws-z", worktreePath: "/wt/z", repoPath: "", agent: "claude", title: "legacy-title",
    branch: "feat/legacy", state: "idle", caps: { approvals: false, attention: false },
    paneId: "pz", lastActive: "",
  }];
  render(Sidebar, { props: { workspaces: ws, activeId: null, onSelect: () => {}, onNew: () => {} } });
  const titleSpan = document.querySelector(".workspace-title")!;
  expect(titleSpan.textContent).toBe("legacy-title");
});

test("empty hint renders when workspaces is empty", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces: [], activeId: null, onSelect: () => {}, onNew: () => {} } });
  expect(screen.getByTestId("sidebar-empty-hint")).toBeInTheDocument();
  expect(screen.getByTestId("sidebar-empty-hint")).toHaveTextContent(/no sessions/i);
});

// ---------------------------------------------------------------------------
// Feature: inline rename (double-click / right-click → input; commit / cancel)
// ---------------------------------------------------------------------------

test("double-click on the title enters an input seeded with the current title", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onEditStart = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, onEditStart } });
  await waitFor(() => screen.getByText("feat-auth"));

  const titleSpan = document.querySelector(".workspace-title")!;
  await fireEvent.dblClick(titleSpan);
  const input = screen.getByLabelText(/rename session/i) as HTMLInputElement;
  expect(input).toBeInTheDocument();
  expect(input.value).toBe("feat-auth");
  expect(onEditStart).toHaveBeenCalled();
});

test("right-click opens the row menu; choosing Rename enters the input", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-auth"));

  // Right-click on a row now opens a Rename/Remove menu (bubbles to the row
  // button's oncontextmenu) instead of jumping straight into rename.
  const titleSpan = document.querySelector(".workspace-title")!;
  await fireEvent.contextMenu(titleSpan);
  const renameItem = screen.getByRole("menuitem", { name: /rename/i });
  expect(renameItem).toBeInTheDocument();
  await fireEvent.click(renameItem);
  expect(screen.getByLabelText(/rename session/i)).toBeInTheDocument();
});

test("committing on Enter calls onRename with the new trimmed title", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onRename = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, onRename } });
  await waitFor(() => screen.getByText("feat-auth"));

  const titleSpan = document.querySelector(".workspace-title")!;
  await fireEvent.dblClick(titleSpan);
  const input = screen.getByLabelText(/rename session/i);
  await fireEvent.input(input, { target: { value: "  Renamed Auth  " } });
  await fireEvent.keyDown(input, { key: "Enter" });
  expect(onRename).toHaveBeenCalledWith("ws_a", "Renamed Auth");
  // Edit mode exits: the input is gone, the title span is back.
  await waitFor(() => expect(screen.queryByLabelText(/rename session/i)).not.toBeInTheDocument());
});

test("Escape cancels the rename without calling onRename", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onRename = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, onRename } });
  await waitFor(() => screen.getByText("feat-auth"));

  const titleSpan = document.querySelector(".workspace-title")!;
  await fireEvent.dblClick(titleSpan);
  const input = screen.getByLabelText(/rename session/i);
  await fireEvent.input(input, { target: { value: "Should Not Commit" } });
  await fireEvent.keyDown(input, { key: "Escape" });
  expect(onRename).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.queryByLabelText(/rename session/i)).not.toBeInTheDocument());
});

test("committing an unchanged title does NOT call onRename", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onRename = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, onRename } });
  await waitFor(() => screen.getByText("feat-auth"));

  const titleSpan = document.querySelector(".workspace-title")!;
  await fireEvent.dblClick(titleSpan);
  const input = screen.getByLabelText(/rename session/i);
  await fireEvent.keyDown(input, { key: "Enter" });
  expect(onRename).not.toHaveBeenCalled();
});

// ---------------------------------------------------------------------------
// F37: per-row remove affordance (hover × + right-click Remove) via requestRemove
// ---------------------------------------------------------------------------

test("F37: a non-active row exposes a remove (×) control wired to requestRemove", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const requestRemove = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, requestRemove } });
  await waitFor(() => screen.getByText("feat-core"));

  // ws_b is a background (non-active) row. The × control is always in the DOM
  // (CSS reveals it on hover/focus), so presence + click are directly testable.
  const removeBtn = screen.getByTestId("row-remove-ws_b") as HTMLButtonElement;
  expect(removeBtn).toBeInTheDocument();
  await fireEvent.click(removeBtn);
  expect(requestRemove).toHaveBeenCalledWith("ws_b");
});

test("F37: the remove (×) control is absent until requestRemove is provided", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-core"));
  expect(screen.queryByTestId("row-remove-ws_b")).toBeNull();
});

test("F37: clicking the × does not also trigger row onSelect", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const onSelect = vi.fn();
  const requestRemove = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect, onNew: () => {}, requestRemove } });
  await waitFor(() => screen.getByText("feat-core"));

  await fireEvent.click(screen.getByTestId("row-remove-ws_b"));
  expect(requestRemove).toHaveBeenCalledWith("ws_b");
  expect(onSelect).not.toHaveBeenCalled();
});

test("F37: right-click opens a Rename/Remove menu; Remove calls requestRemove and closes the menu", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const requestRemove = vi.fn();
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {}, requestRemove } });
  await waitFor(() => screen.getByText("feat-auth"));

  const titleSpan = document.querySelector(".workspace-title")!; // first row = ws_a
  await fireEvent.contextMenu(titleSpan);

  expect(screen.getByRole("menu")).toBeInTheDocument();
  expect(screen.getByRole("menuitem", { name: /rename/i })).toBeInTheDocument();
  const removeItem = screen.getByRole("menuitem", { name: /remove/i });
  expect(removeItem).toBeInTheDocument();

  await fireEvent.click(removeItem);
  expect(requestRemove).toHaveBeenCalledWith("ws_a");
  await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
});

test("F37: the menu's Remove item is absent when requestRemove is not provided", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-auth"));

  const titleSpan = document.querySelector(".workspace-title")!;
  await fireEvent.contextMenu(titleSpan);
  expect(screen.getByRole("menuitem", { name: /rename/i })).toBeInTheDocument();
  expect(screen.queryByRole("menuitem", { name: /remove/i })).toBeNull();
});

// ---------------------------------------------------------------------------
// F38: distinct done color + visible compact status word on attention/active rows
// ---------------------------------------------------------------------------

test("F38: done and running status icons resolve to different color tokens", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  // jsdom cannot resolve custom properties, so compare the raw color tokens each
  // status class maps to in the injected component CSS.
  const styleText = [...document.querySelectorAll("style")].map((s) => s.textContent).join("\n");
  const colorOf = (cls: string) =>
    new RegExp(`\\.${cls}[^{}]*\\{[^{}]*?color:\\s*(var\\(--perch-[a-z0-9-]+\\))`).exec(styleText)?.[1];
  const running = colorOf("status-running");
  const done = colorOf("status-done");
  expect(running).toBeTruthy();
  expect(done).toBeTruthy();
  expect(done).not.toBe(running);
});

test("F38: attention rows show a visible compact status word; calm rows keep it sr-only", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });

  // ws_c is awaiting-approval (attention) → visible .status-text "needs you".
  const approvalBtn = screen.getByRole("button", { name: /bug-fix/ });
  const stateText = approvalBtn.querySelector(".status-text");
  expect(stateText).toBeInTheDocument();
  expect(stateText!.textContent).toBe("needs you");
  expect(approvalBtn.querySelector(".status-label")).toBeNull();

  // ws_b is idle + not active → no visible word, sr-only label retained.
  const idleBtn = screen.getByRole("button", { name: /feat-core/ });
  expect(idleBtn.querySelector(".status-text")).toBeNull();
  expect(idleBtn.querySelector(".status-label")).toBeInTheDocument();
});

test("F38: the active row shows its state as a visible compact word", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  const activeBtn = screen.getByRole("button", { name: /feat-auth/ });
  expect(activeBtn.querySelector(".status-text")!.textContent).toBe("running");
});

// ---------------------------------------------------------------------------
// F45: approval pulse decays to a steady dot (finite iterations, not infinite)
// ---------------------------------------------------------------------------

test("F45: awaiting-approval pulse decays to a steady dot (finite iterations)", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  const styleText = [...document.querySelectorAll("style")].map((s) => s.textContent).join("\n");
  const rule = /\.status-awaiting-approval[^{}]*\{([^}]*perch-attn-pulse[^}]*)\}/.exec(styleText)?.[1] ?? "";
  expect(rule).toContain("perch-attn-pulse");
  expect(rule).not.toContain("infinite");
});

// ---------------------------------------------------------------------------
// F49a: sidebar last-active uses the shared formatRelativeAge helper
// ---------------------------------------------------------------------------

test("F49a: sidebar last-active renders the shared formatRelativeAge output", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const { formatRelativeAge } = await import("./constants");
  const iso = new Date(Date.now() - 3 * 86400000).toISOString();
  const ws: WorkspaceVM[] = [{
    id: "ws-age", worktreePath: "/wt/age", repoPath: "/home/me/agerepo", agent: "claude", title: "age-test",
    branch: "feat/age", state: "idle", caps: { approvals: false, attention: false }, paneId: "pa", lastActive: iso,
  }];
  render(Sidebar, { props: { workspaces: ws, activeId: null, onSelect: () => {}, onNew: () => {} } });
  const ageSpan = document.querySelector(".workspace-age")!;
  expect(ageSpan.textContent).toBe(formatRelativeAge(iso));
  expect(ageSpan.textContent).toBe("3d ago");
});

// ---------------------------------------------------------------------------
// Row-level attention signal: a BACKGROUND (non-active) row in an
// awaiting/errored/done state begs for a look via .attn + a per-urgency class.
// The active row never gets it (opening the session IS the acknowledgement).
//
// NOTE: the actual bar/tint/glow + the slow pulse are pure CSS on a ::before
// pseudo-element. jsdom has no layout or animation engine, so these tests
// verify only the class/marker wiring and the injected CSS text — the visible
// treatment and its motion are manual-smoke-only.
// ---------------------------------------------------------------------------

test("attn: background rows in awaiting/errored/done states get .attn + the per-urgency class", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  // ws_a active → every other attention-state row is a background row.
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });

  const cases: Array<[RegExp, string]> = [
    [/bug-fix/,       "attn-awaiting-approval"], // ws_c awaiting-approval
    [/asking-work/,   "attn-awaiting-input"],    // ws_q awaiting-input
    [/errored-work/,  "attn-errored"],           // ws_e errored
    [/done-work/,     "attn-done"],              // ws_d done
  ];
  for (const [name, urgencyClass] of cases) {
    const row = screen.getByRole("button", { name });
    expect(row.classList.contains("attn")).toBe(true);
    expect(row.classList.contains(urgencyClass)).toBe(true);
  }
});

test("attn: the ACTIVE row never gets the attention treatment, even in an attention state", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  // Make the awaiting-approval session the active one — viewing it clears the beg.
  render(Sidebar, { props: { workspaces, activeId: "ws_c", onSelect: () => {}, onNew: () => {} } });

  const activeRow = screen.getByRole("button", { name: /bug-fix/ });
  expect(activeRow.classList.contains("attn")).toBe(false);
  expect(activeRow.classList.contains("attn-awaiting-approval")).toBe(false);
});

test("attn: calm background rows (idle/running) do NOT get the attention treatment", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });

  const idleRow = screen.getByRole("button", { name: /feat-core/ }); // ws_b idle, background
  expect(idleRow.classList.contains("attn")).toBe(false);
});

test("attn: an already-seen awaiting-input row (in ackedInputIds) stops begging", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  // ws_q is awaiting-input and backgrounded, but its question was acknowledged →
  // displayState collapses it to "idle", so it should not carry .attn.
  render(Sidebar, {
    props: {
      workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {},
      ackedInputIds: new Set(["ws_q"]),
    },
  });
  const acked = screen.getByRole("button", { name: /asking-work/ });
  expect(acked.classList.contains("attn")).toBe(false);
  expect(acked.classList.contains("attn-awaiting-input")).toBe(false);
});

test("attn CSS: each urgency maps to its color token; done is calmer (finite, not infinite)", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  const styleText = [...document.querySelectorAll("style")].map((s) => s.textContent).join("\n");

  const attnColorOf = (cls: string) =>
    new RegExp(`\\.${cls}[^{}]*\\{[^{}]*?--attn-color:\\s*(var\\(--perch-[a-z0-9-]+\\))`).exec(styleText)?.[1];
  expect(attnColorOf("attn-awaiting-approval")).toBe("var(--perch-warn)");
  expect(attnColorOf("attn-awaiting-input")).toBe("var(--perch-info)");
  expect(attnColorOf("attn-errored")).toBe("var(--perch-err)");
  expect(attnColorOf("attn-done")).toBe("var(--perch-ok)");

  // The base pulse (awaiting/errored) runs continuously until viewed…
  // ([^{}]* swallows Svelte's injected .svelte-hash scope class before ::before;
  //  (?=[.:]) keeps this off the .attn-done rule, whose next char is '-'.)
  const base = /\.workspace-row\.attn(?=[.:])[^{}]*::before[^{}]*\{([^}]*)\}/.exec(styleText)?.[1] ?? "";
  expect(base).toContain("perch-attn-row");
  expect(base).toContain("infinite");
  // …but "done" settles after a finite, gentler breath (never infinite).
  const doneRule = /\.workspace-row\.attn-done[^{}]*::before[^{}]*\{([^}]*)\}/.exec(styleText)?.[1] ?? "";
  expect(doneRule).toContain("perch-attn-row");
  expect(doneRule).not.toContain("infinite");
});

test("attn contrast: the wash sits BEHIND row content (isolated context + negative-z pseudo) and no longer overlays the --row-color edge", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  const styleText = [...document.querySelectorAll("style")].map((s) => s.textContent).join("\n");

  // The row establishes its own stacking context so the tint pseudo can be pushed
  // behind the in-flow text/icon without escaping behind the row's own background.
  const rowRule = /\.workspace-row\.attn(?=[.:])[^{}]*\{([^}]*)\}/.exec(styleText)?.[1] ?? "";
  expect(rowRule).toMatch(/isolation:\s*isolate/);

  // The ::before tint/glow paints at a negative z-index — above the row background
  // but BELOW the text/icon, which keep full contrast. jsdom has no compositor, so
  // this asserts the wiring only; the RENDERED contrast is manual-smoke.
  const beforeRule = /\.workspace-row\.attn(?=[.:])[^{}]*::before[^{}]*\{([^}]*)\}/.exec(styleText)?.[1] ?? "";
  expect(beforeRule).toMatch(/z-index:\s*-1/);
  // inset:0 fills only the padding box, so the 3px per-worktree --row-color border
  // is never covered by the attn wash (the old `inset: 0 0 0 -3px` overlaid it).
  expect(beforeRule).not.toContain("-3px");
});

test("attn CSS: prefers-reduced-motion disables the row pulse (static bar/tint fallback)", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  const styleText = [...document.querySelectorAll("style")].map((s) => s.textContent).join("\n");

  // The grouped attn::before + attn-done::before selector with animation:none
  // appears ONLY in the reduced-motion guard (the standalone base rule uses a
  // single selector), so matching the comma-joined pair is a robust proxy.
  expect(styleText).toMatch(/prefers-reduced-motion/);
  expect(styleText).toMatch(
    /\.workspace-row\.attn[^{}]*::before\s*,\s*\.workspace-row\.attn-done[^{}]*::before\s*\{[^}]*animation:\s*none/,
  );
});

test("F32: exited state renders the ⏻ icon with status-exited class + 'exited' label (dim, NOT error)", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const ws: WorkspaceVM[] = [{
    id: "ws-x", worktreePath: "/wt/x", repoPath: "/repo/repo-exited", agent: "claude", title: "exited-work",
    branch: "feat/exit", state: "exited", caps: { approvals: false, attention: false }, paneId: "px", lastActive: "",
  }];
  render(Sidebar, { props: { workspaces: ws, activeId: "ws-x", onSelect: () => {}, onNew: () => {} } });

  const btn = screen.getByRole("button", { name: /exited-work/ });
  const icon = btn.querySelector(".status-icon")!;
  expect(icon).toBeInTheDocument();
  expect(icon.classList.contains("status-exited")).toBe(true);
  expect(icon.textContent).toContain("⏻");
  expect(btn).toHaveTextContent("exited");
  // A terminal exit is NEUTRAL, not a red error.
  expect(icon.classList.contains("status-errored")).toBe(false);
});
