import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("renders message when open", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  render(ConfirmDialog, {
    props: {
      open: true,
      message: 'Kill agent "feat-x"? The session and its agent will be terminated.',
      confirmLabel: "Kill",
      onConfirm,
      onCancel,
    },
  });
  await waitFor(() =>
    expect(
      screen.getByText('Kill agent "feat-x"? The session and its agent will be terminated.')
    ).toBeInTheDocument()
  );
});

test("clicking confirm calls onConfirm", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  const onConfirm = vi.fn();
  render(ConfirmDialog, {
    props: { open: true, message: "Are you sure?", confirmLabel: "Kill", onConfirm },
  });
  await waitFor(() => screen.getByRole("button", { name: "Kill" }));
  await fireEvent.click(screen.getByRole("button", { name: "Kill" }));
  expect(onConfirm).toHaveBeenCalledTimes(1);
});

test("clicking cancel calls onCancel", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  const onCancel = vi.fn();
  render(ConfirmDialog, {
    props: { open: true, message: "Are you sure?", onCancel },
  });
  await waitFor(() => screen.getByRole("button", { name: "Cancel" }));
  await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onCancel).toHaveBeenCalledTimes(1);
});

test("does not render when closed", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  render(ConfirmDialog, {
    props: { open: false, message: "Should not appear" },
  });
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("note prop renders when provided", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  render(ConfirmDialog, { props: {
    open: true, message: "Remove workspace?", destructive: true,
    note: "Removes this session from perch. The worktree and its files remain on disk.",
    onConfirm: () => {}, onCancel: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /confirm/i }));
  expect(screen.getByText("Removes this session from perch. The worktree and its files remain on disk.")).toBeInTheDocument();
  // No hardcoded undo text
  expect(screen.queryByText(/can be undone/i)).not.toBeInTheDocument();
});

test("no hardcoded undo text when destructive=true but no note", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  render(ConfirmDialog, { props: {
    open: true, message: "Remove workspace?", destructive: true,
    onConfirm: () => {}, onCancel: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /confirm/i }));
  expect(screen.queryByText(/can be undone/i)).not.toBeInTheDocument();
  expect(screen.queryByText(/undo/i)).not.toBeInTheDocument();
});
