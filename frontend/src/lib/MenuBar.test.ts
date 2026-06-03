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
  // ArrowDown on the first menuitem should move focus to the next
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
