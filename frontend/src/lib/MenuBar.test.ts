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
