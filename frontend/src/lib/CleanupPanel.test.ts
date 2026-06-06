import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi } from "vitest";
import type { StaleSessionVM } from "./wails";

vi.mock("./wails", () => ({
  cleanupSessions: vi.fn(async () => {}),
  listStaleSessions: vi.fn(async () => []),
}));

function makeSession(overrides: Partial<StaleSessionVM> = {}): StaleSessionVM {
  return {
    id: "ws-1", title: "feat-old", branch: "feat/old", agent: "claude",
    lastActive: new Date(Date.now() - 32 * 86400000).toISOString(),
    added: 5, removed: 2, clean: true, merged: true, safe: true,
    ...overrides,
  };
}

test("renders one row per stale session with branch agent and diffstat", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [
    makeSession({ id: "ws-1", branch: "feat/old", agent: "claude", added: 5, removed: 2 }),
    makeSession({ id: "ws-2", branch: "feat/also-old", agent: "opencode", added: 0, removed: 0, safe: false, merged: false }),
  ];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  expect(screen.getByText(/feat\/old/)).toBeInTheDocument();
  expect(screen.getByText(/feat\/also-old/)).toBeInTheDocument();
  expect(screen.getByText(/claude/)).toBeInTheDocument();
  expect(screen.getByText(/opencode/)).toBeInTheDocument();
  expect(screen.getByText(/\+5/)).toBeInTheDocument();
  expect(screen.getByText(/−2/)).toBeInTheDocument();
});

test("safe rows are default-checked; unsafe rows are unchecked", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [
    makeSession({ id: "ws-safe", safe: true }),
    makeSession({ id: "ws-unsafe", safe: false, merged: false }),
  ];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  const safeBox   = screen.getByTestId("row-check-ws-safe")   as HTMLInputElement;
  const unsafeBox = screen.getByTestId("row-check-ws-unsafe") as HTMLInputElement;
  expect(safeBox.checked).toBe(true);
  expect(unsafeBox.checked).toBe(false);
});

test("unsafe row shows warning badge", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [makeSession({ id: "ws-u", safe: false, merged: false, clean: false })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  expect(screen.getByText("⚠")).toBeInTheDocument();
});

test("Select all checks all rows including unsafe", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [
    makeSession({ id: "ws-1", safe: true }),
    makeSession({ id: "ws-2", safe: false, merged: false }),
  ];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  const selectAll = screen.getByRole("checkbox", { name: /select all/i });
  await fireEvent.click(selectAll);
  const box1 = screen.getByTestId("row-check-ws-1") as HTMLInputElement;
  const box2 = screen.getByTestId("row-check-ws-2") as HTMLInputElement;
  expect(box1.checked).toBe(true);
  expect(box2.checked).toBe(true);
});

test("Remove selected calls cleanupSessions with checked ids", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const { cleanupSessions } = await import("./wails");
  const sessions = [
    makeSession({ id: "ws-a", safe: true }),
    makeSession({ id: "ws-b", safe: false, merged: false }),
  ];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  const removeBtn = screen.getByRole("button", { name: /remove selected/i });
  await fireEvent.click(removeBtn);
  const confirmBtn = screen.getByRole("button", { name: /^remove$/i });
  await fireEvent.click(confirmBtn);
  await waitFor(() => expect(cleanupSessions).toHaveBeenCalledWith(["ws-a"], false));
});

test("Open button calls onOpen with the session id", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const onOpen = vi.fn();
  const sessions = [makeSession({ id: "ws-open" })];
  render(CleanupPanel, { props: { sessions, onClose: () => {}, onOpen } });
  const openBtn = screen.getByRole("button", { name: /^open$/i });
  await fireEvent.click(openBtn);
  expect(onOpen).toHaveBeenCalledWith("ws-open");
});
