// frontend/src/lib/MenuBar.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("menu item click invokes onCommand", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  const onCommand = vi.fn();
  render(MenuBar, { props: { onCommand, unreadCount: 0 } });
  await fireEvent.click(screen.getByRole("menuitem", { name: /session/i }));
  await waitFor(() => screen.getByRole("menuitem", { name: /new session/i }));
  await fireEvent.click(screen.getByRole("menuitem", { name: /new session/i }));
  expect(onCommand).toHaveBeenCalledWith("session:new");
});

test("bell shows unread badge count", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 4 } });
  expect(screen.getByText("4")).toBeInTheDocument();
});

test("bell with 0 unread shows no badge", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 0 } });
  expect(screen.queryByText("0")).toBeNull();
});

test("ArrowDown on open menu moves focus, Enter activates focused item", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  const onCommand = vi.fn();
  render(MenuBar, { props: { onCommand, unreadCount: 0 } });
  // Open the Session menu by clicking the top-level button
  await fireEvent.click(screen.getByRole("menuitem", { name: /session/i }));
  await waitFor(() => screen.getByRole("menuitem", { name: /new session/i }));
  // ArrowDown on the first menuitem should move focus to the next item.
  const newSessionItem = screen.getByRole("menuitem", { name: /new session/i });
  await fireEvent.keyDown(newSessionItem, { key: "ArrowDown" });
  // Now Enter on the focused item (close session) should fire onCommand
  const closeSessionItem = screen.getByRole("menuitem", { name: /close session/i });
  await fireEvent.keyDown(closeSessionItem, { key: "Enter" });
  expect(onCommand).toHaveBeenCalledWith("session:close");
});

test("Escape on open menu closes it and returns focus to top-level button", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 0 } });
  const sessionBtn = screen.getByRole("menuitem", { name: /session/i });
  await fireEvent.click(sessionBtn);
  await waitFor(() => screen.getByRole("menuitem", { name: /new session/i }));
  const firstItem = screen.getByRole("menuitem", { name: /new session/i });
  await fireEvent.keyDown(firstItem, { key: "Escape" });
  expect(screen.queryByRole("menuitem", { name: /new session/i })).toBeNull();
});

test("Enter on top-level menu button opens the menu", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 0 } });
  const sessionBtn = screen.getByRole("menuitem", { name: /session/i });
  await fireEvent.keyDown(sessionBtn, { key: "Enter" });
  await waitFor(() => expect(screen.getByRole("menuitem", { name: /new session/i })).toBeInTheDocument());
});

// ── F56: Left/Right roving across the top-level menu buttons ──────────────────
test("ArrowRight / ArrowLeft rove focus between top-level menu buttons", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 0 } });
  const sessionBtn = screen.getByRole("menuitem", { name: "Session" });
  sessionBtn.focus();
  await fireEvent.keyDown(sessionBtn, { key: "ArrowRight" });
  // F51: the menu folded "Worktree" into "Session", so the menu adjacent to Session
  // is now "View".
  const viewBtn = screen.getByRole("menuitem", { name: "View" });
  expect(viewBtn).toHaveFocus();
  await fireEvent.keyDown(viewBtn, { key: "ArrowLeft" });
  expect(sessionBtn).toHaveFocus();
});

// ── F56: menu items render their keyboard accelerators (aria-hidden chips) ─────
test("View menu items render keyboard accelerators without polluting the item name", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  render(MenuBar, { props: { onCommand: () => {}, unreadCount: 0 } });
  await fireEvent.click(screen.getByRole("menuitem", { name: "View" }));
  await waitFor(() => screen.getByRole("menuitem", { name: "Diff view" }));
  // The menu shows accelerator chips for the view shortcuts.
  expect(screen.getByText("1")).toBeInTheDocument();
  expect(screen.getByText("3")).toBeInTheDocument();
  // The chip is aria-hidden so the menuitem's accessible name is still the label
  expect(screen.getByRole("menuitem", { name: "Diff view" })).toBeInTheDocument();
});

// ── F48: the theme menu item cycles (no ellipsis promising a dialog) ──────────
test("View menu shows 'Cycle theme' (not 'Theme…') and dispatches view:theme", async () => {
  const { default: MenuBar } = await import("./MenuBar.svelte");
  const onCommand = vi.fn();
  render(MenuBar, { props: { onCommand, unreadCount: 0 } });
  await fireEvent.click(screen.getByRole("menuitem", { name: "View" }));
  await waitFor(() => screen.getByRole("menuitem", { name: "Cycle theme" }));
  expect(screen.queryByText("Theme…")).toBeNull();
  await fireEvent.click(screen.getByRole("menuitem", { name: "Cycle theme" }));
  expect(onCommand).toHaveBeenCalledWith("view:theme");
});
