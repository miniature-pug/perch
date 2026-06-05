// frontend/src/lib/Sidebar.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";
import type { WorkspaceVM } from "./wails";

const workspaces: WorkspaceVM[] = [
  { id: "ws_a", worktreePath: "/wt/a", agent: "claude", title: "feat-auth",
    branch: "feat/auth", state: "running", caps: { approvals: false, attention: false }, paneId: "p1", lastActive: "" },
  { id: "ws_b", worktreePath: "/wt/b", agent: "claude", title: "feat-core",
    branch: "feat/core", state: "idle", caps: { approvals: false, attention: false }, paneId: "p2", lastActive: "" },
  { id: "ws_c", worktreePath: "/wt/c", agent: "claude", title: "bug-fix",
    branch: "fix/crash", state: "awaiting-approval", caps: { approvals: true, attention: false }, paneId: "p3", lastActive: "" },
  { id: "ws_d", worktreePath: "/wt/d", agent: "claude", title: "done-work",
    branch: "feat/done", state: "done", caps: { approvals: false, attention: false }, paneId: "p4", lastActive: "" },
  { id: "ws_e", worktreePath: "/wt/e", agent: "claude", title: "errored-work",
    branch: "feat/err", state: "errored", caps: { approvals: false, attention: false }, paneId: "p5", lastActive: "" },
  { id: "ws_q", worktreePath: "/wt/q", agent: "claude", title: "asking-work",
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
// Feature 1: diffStats prop — +/− render in sidebar rows
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
// Feature: count-up — final values in diffstat
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
// Feature: review pill — files count
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

test("workspace-row carries --row-color style based on ws.id", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const { worktreeColor } = await import("./constants");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-auth"));

  const btn = screen.getByRole("button", { name: /feat-auth/ }) as HTMLElement;
  const expected = worktreeColor("ws_a");
  // style:--row-color is set as a CSS custom property on the element's inline style
  const styleAttr = btn.getAttribute("style") ?? "";
  expect(styleAttr).toContain(expected);
});

test("workspace-row --row-color is stable and deterministic per id", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const { worktreeColor } = await import("./constants");
  render(Sidebar, { props: { workspaces, activeId: "ws_a", onSelect: () => {}, onNew: () => {} } });
  await waitFor(() => screen.getByText("feat-core"));

  const btnA = screen.getByRole("button", { name: /feat-auth/ }) as HTMLElement;
  const btnB = screen.getByRole("button", { name: /feat-core/ }) as HTMLElement;
  expect(btnA.getAttribute("style") ?? "").toContain(worktreeColor("ws_a"));
  expect(btnB.getAttribute("style") ?? "").toContain(worktreeColor("ws_b"));
});
