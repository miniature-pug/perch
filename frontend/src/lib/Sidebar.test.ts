// frontend/src/lib/Sidebar.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const workspaces = [
  { id: "ws_a", worktreePath: "/wt/a", agent: "claude", title: "feat-auth",
    branch: "feat/auth", state: "running", caps: {}, paneId: "p1", lastActive: "" },
  { id: "ws_b", worktreePath: "/wt/b", agent: "claude", title: "feat-core",
    branch: "feat/core", state: "idle", caps: {}, paneId: "p2", lastActive: "" },
  { id: "ws_c", worktreePath: "/wt/c", agent: "claude", title: "bug-fix",
    branch: "fix/crash", state: "awaiting-approval", caps: {}, paneId: "p3", lastActive: "" },
  { id: "ws_d", worktreePath: "/wt/d", agent: "claude", title: "done-work",
    branch: "feat/done", state: "done", caps: {}, paneId: "p4", lastActive: "" },
  { id: "ws_e", worktreePath: "/wt/e", agent: "claude", title: "errored-work",
    branch: "feat/err", state: "errored", caps: {}, paneId: "p5", lastActive: "" },
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
