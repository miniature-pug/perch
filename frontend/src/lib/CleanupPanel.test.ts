import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi } from "vitest";
import type { StaleSessionVM } from "./wails";

vi.mock("./wails", () => ({
  cleanupSessions: vi.fn(async () => {}),
  listStaleSessions: vi.fn(async () => []),
}));

// The mocked module is shared across every test in this file (vi.mock is
// hoisted once), so call history must be reset between tests — otherwise a
// "not called" assertion in a later test can see calls left over from an
// earlier one.
beforeEach(() => {
  vi.clearAllMocks();
});

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

// ---------------------------------------------------------------------------
// WIN #4: force-remove path for checked unsafe (dirty/unmerged) rows.
// Remove selected must NEVER force; force-remove is a distinct, separately
// gated control so a single misclick cannot discard uncommitted changes or
// unmerged commits.
// ---------------------------------------------------------------------------

test("Remove selected only removes checked SAFE rows with force=false, even when an unsafe row is also checked", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const { cleanupSessions } = await import("./wails");
  const sessions = [
    makeSession({ id: "ws-a", safe: true }),
    makeSession({ id: "ws-b", safe: false, merged: false }),
  ];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  // Manually check the unsafe row too.
  await fireEvent.click(screen.getByTestId("row-check-ws-b"));
  const removeBtn = screen.getByRole("button", { name: /remove selected/i });
  await fireEvent.click(removeBtn);
  const confirmBtn = screen.getByRole("button", { name: /^remove$/i });
  await fireEvent.click(confirmBtn);
  await waitFor(() => expect(cleanupSessions).toHaveBeenCalledWith(["ws-a"], false));
  expect(cleanupSessions).not.toHaveBeenCalledWith(expect.arrayContaining(["ws-b"]), expect.anything());
  expect(cleanupSessions).not.toHaveBeenCalledWith(expect.anything(), true);
});

test("Remove selected is disabled when only unsafe rows are checked (unsafe rows cannot go through the normal path)", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [makeSession({ id: "ws-u", safe: false, merged: false })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  await fireEvent.click(screen.getByTestId("row-check-ws-u"));
  const removeBtn = screen.getByRole("button", { name: /remove selected/i }) as HTMLButtonElement;
  expect(removeBtn.disabled).toBe(true);
});

test("Force remove unsafe control is hidden when there are no unsafe sessions", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [makeSession({ id: "ws-a", safe: true })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  expect(screen.queryByRole("button", { name: /force remove/i })).toBeNull();
});

test("Force remove unsafe control is disabled until an unsafe row is checked", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [makeSession({ id: "ws-u", safe: false, merged: false })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  const forceBtn = screen.getByRole("button", { name: /force remove unsafe/i }) as HTMLButtonElement;
  expect(forceBtn.disabled).toBe(true);
  await fireEvent.click(screen.getByTestId("row-check-ws-u"));
  expect(forceBtn.disabled).toBe(false);
});

test("Force remove unsafe requires an explicit confirmation before calling cleanupSessions with force=true", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const { cleanupSessions } = await import("./wails");
  const sessions = [makeSession({ id: "ws-u", safe: false, merged: false })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  await fireEvent.click(screen.getByTestId("row-check-ws-u"));
  const forceBtn = screen.getByRole("button", { name: /force remove unsafe/i });
  await fireEvent.click(forceBtn);
  // Clicking the trigger alone must not call the backend — the confirmation
  // dialog must appear and require its own click.
  expect(cleanupSessions).not.toHaveBeenCalled();
  const confirmBtn = await screen.findByRole("button", { name: /^force remove$/i });
  await fireEvent.click(confirmBtn);
  await waitFor(() => expect(cleanupSessions).toHaveBeenCalledWith(["ws-u"], true));
});

test("the force-remove confirmation states plainly that it discards uncommitted changes and unmerged commits", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [makeSession({ id: "ws-u", safe: false, merged: false })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  await fireEvent.click(screen.getByTestId("row-check-ws-u"));
  await fireEvent.click(screen.getByRole("button", { name: /force remove unsafe/i }));
  await screen.findByRole("button", { name: /^force remove$/i });
  expect(screen.getByText(/uncommitted changes/i)).toBeInTheDocument();
  expect(screen.getByText(/unmerged commits/i)).toBeInTheDocument();
});

test("dismissing the force-remove confirmation does not call cleanupSessions", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const { cleanupSessions } = await import("./wails");
  const sessions = [makeSession({ id: "ws-u", safe: false, merged: false })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  await fireEvent.click(screen.getByTestId("row-check-ws-u"));
  await fireEvent.click(screen.getByRole("button", { name: /force remove unsafe/i }));
  const cancelBtn = await screen.findByRole("button", { name: /^cancel$/i });
  await fireEvent.click(cancelBtn);
  expect(cleanupSessions).not.toHaveBeenCalled();
  // The dialog should be gone.
  expect(screen.queryByRole("button", { name: /^force remove$/i })).toBeNull();
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

// ---------------------------------------------------------------------------
// F49a: last-active uses the shared formatRelativeAge helper (was a divergent
// local formatRelative that rendered "yesterday" and lacked the empty guard).
// ---------------------------------------------------------------------------

test("F49a: last-active renders the shared helper output ('1d ago', not 'yesterday')", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const { formatRelativeAge } = await import("./constants");
  const iso = new Date(Date.now() - 1 * 86400000).toISOString();
  const sessions = [makeSession({ id: "ws-1d", lastActive: iso })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  const ageCell = document.querySelector(".cleanup-age")!;
  expect(ageCell.textContent).toBe(formatRelativeAge(iso));
  expect(ageCell.textContent).toBe("1d ago");
  expect(ageCell.textContent).not.toContain("yesterday");
});

test("F49a: last-active guards empty/invalid dates (renders empty, not 'NaN')", async () => {
  const { default: CleanupPanel } = await import("./CleanupPanel.svelte");
  const sessions = [makeSession({ id: "ws-empty", lastActive: "" })];
  render(CleanupPanel, { props: { sessions, onClose: () => {} } });
  const ageCell = document.querySelector(".cleanup-age")!;
  expect(ageCell.textContent).toBe("");
});
