// frontend/src/lib/NotificationHub.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const items = [
  { id: "n1", workspaceId: "ws_a", tier: "blocking" as const, kind: "approval" as const, title: "Approve bash", body: "run ls /tmp", read: false, ts: 1 },
  { id: "n2", workspaceId: "ws_b", tier: "ambient"  as const, kind: "done"     as const, title: "Turn done",    body: "finished",   read: false, ts: 2 },
];

test("filter approvals shows only approval-kind items", async () => {
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
    { id: "app1", workspaceId: "", tier: "blocking" as const, kind: "error" as const, title: "Failed to create session", body: "boom", read: false, ts: 1 },
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

// -------------------------------------------------------------------------
// F40 — self-close: Escape and outside-click ask the parent to close.
// -------------------------------------------------------------------------

test("F40: Escape asks the parent to close the hub (onClose)", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onClose = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {}, onClose } });
  await waitFor(() => screen.getByText("Approve bash"));
  await fireEvent.keyDown(document.body, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
});

test("F40: a click outside the panel closes the hub; a click inside does not", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const onClose = vi.fn();
  render(NotificationHub, { props: { items, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {}, onClose } });
  await waitFor(() => screen.getByText("Approve bash"));

  // A click inside the panel (a filter button) must NOT close it.
  await fireEvent.click(screen.getByRole("button", { name: /^all$/i }));
  expect(onClose).not.toHaveBeenCalled();

  // A click on the backdrop / anywhere outside the panel closes it.
  await fireEvent.click(document.body);
  expect(onClose).toHaveBeenCalledTimes(1);
});

// -------------------------------------------------------------------------
// F41 — filter on the explicit kind; empty text reflects the active filter.
// -------------------------------------------------------------------------

test("F41: a blocking error is NOT under the Approvals filter, but is under Errors", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const mixed = [
    // Both are tier "blocking" — only the kind separates the error from the approval.
    { id: "e1", workspaceId: "ws_a", tier: "blocking" as const, kind: "error"    as const, title: "Stage failed",    body: "Could not stage hunk", read: false, ts: 2 },
    { id: "a1", workspaceId: "ws_b", tier: "blocking" as const, kind: "approval" as const, title: "Approval needed", body: "run bash",             read: false, ts: 1 },
  ];
  render(NotificationHub, { props: { items: mixed, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {} } });

  await fireEvent.click(screen.getByRole("button", { name: /approvals/i }));
  await waitFor(() => expect(screen.getByText("Approval needed")).toBeInTheDocument());
  expect(screen.queryByText("Stage failed")).toBeNull();

  await fireEvent.click(screen.getByRole("button", { name: /errors/i }));
  await waitFor(() => expect(screen.getByText("Stage failed")).toBeInTheDocument());
  expect(screen.queryByText("Approval needed")).toBeNull();
});

test("F41: empty state text reflects the active filter", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  const approvalsOnly = [
    { id: "a1", workspaceId: "ws_a", tier: "blocking" as const, kind: "approval" as const, title: "Approval needed", body: "run bash", read: false, ts: 1 },
  ];
  render(NotificationHub, { props: { items: approvalsOnly, dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {} } });

  await fireEvent.click(screen.getByRole("button", { name: /errors/i }));
  expect(screen.getByText("No errors")).toBeInTheDocument();

  await fireEvent.click(screen.getByRole("button", { name: /done/i }));
  expect(screen.getByText("No completed notifications")).toBeInTheDocument();
});

test("F41: an empty item list shows 'No notifications' under the default filter", async () => {
  const { default: NotificationHub } = await import("./NotificationHub.svelte");
  render(NotificationHub, { props: { items: [], dnd: false, onDismiss: () => {}, onToggleDnd: () => {}, onClearRead: () => {} } });
  expect(screen.getByText("No notifications")).toBeInTheDocument();
});
