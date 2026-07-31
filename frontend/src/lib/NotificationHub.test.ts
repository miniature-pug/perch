// frontend/src/lib/NotificationHub.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const items = [
  { id: "n1", workspaceId: "ws_a", tier: "blocking" as const, title: "Approve bash", body: "run ls /tmp", read: false, ts: 1 },
  { id: "n2", workspaceId: "ws_b", tier: "ambient"  as const, title: "Turn done",    body: "finished",   read: false, ts: 2 },
];

test("filter approvals shows only blocking items", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {} } });
  await fireEvent.click(screen.getByRole("button", { name: /approvals/i }));
  await waitFor(() => expect(screen.getByText("Approve bash")).toBeInTheDocument());
  expect(screen.queryByText("Turn done")).toBeNull();
});

test("dismiss button calls onDismiss with item id", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onDismiss = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss, onToggleDnd: () => {}, onClearRead: () => {} } });
  await waitFor(() => screen.getAllByRole("button", { name: /dismiss/i }));
  await fireEvent.click(screen.getAllByRole("button", { name: /dismiss/i })[0]);
  expect(onDismiss).toHaveBeenCalledWith("n1");
});

test("DND toggle button calls onToggleDnd", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onToggleDnd = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd, onClearRead: () => {} } });
  await fireEvent.click(screen.getByRole("button", { name: /do not disturb/i }));
  expect(onToggleDnd).toHaveBeenCalled();
});

test("clicking a notification's title/body invokes onSelect with its workspaceId", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onSelect = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {}, onSelect } });
  await waitFor(() => screen.getByText("Approve bash"));
  // The navigable area is a button labelled "open session for <title>".
  await fireEvent.click(screen.getByRole("button", { name: /open session for Approve bash/i }));
  expect(onSelect).toHaveBeenCalledWith("ws_a");
});

test("dismiss ✕ does NOT navigate (stops propagation) — only onDismiss fires", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onSelect = vi.fn();
  const onDismiss = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss, onToggleDnd: () => {}, onClearRead: () => {}, onSelect } });
  await waitFor(() => screen.getAllByRole("button", { name: /dismiss/i }));
  await fireEvent.click(screen.getAllByRole("button", { name: /dismiss/i })[0]);
  expect(onDismiss).toHaveBeenCalledWith("n1");
  expect(onSelect).not.toHaveBeenCalled();
});

test("app-level notice with empty workspaceId is NOT a navigation button", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onSelect = vi.fn();
  const appNotice = [
    { id: "app1", workspaceId: "", tier: "blocking" as const, title: "Failed to create session", body: "boom", read: false, ts: 1 },
  ];
  render(NotificationHub, { props: { items: appNotice, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {}, onSelect } });
  await waitFor(() => screen.getByText("Failed to create session"));
  // No navigation button rendered for an empty-workspaceId notice.
  expect(screen.queryByRole("button", { name: /open session for/i })).toBeNull();
});

test("notif-item carries --item-color style matching worktreeColor for its workspaceId", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const { worktreeColor } = await import("./constants");
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {} } });
  await waitFor(() => screen.getByText("Approve bash"));

  const notifItems = document.querySelectorAll(".notif-item");
  expect(notifItems.length).toBeGreaterThanOrEqual(2);

  const item1 = notifItems[0] as HTMLElement;
  const item2 = notifItems[1] as HTMLElement;
  const style1 = item1.getAttribute("style") ?? "";
  const style2 = item2.getAttribute("style") ?? "";
  expect(style1).toContain(worktreeColor("ws_a"));
  expect(style2).toContain(worktreeColor("ws_b"));
});
