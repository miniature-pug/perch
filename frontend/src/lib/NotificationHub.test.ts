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
