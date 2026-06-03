import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("renders message when open", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  const onconfirm = vi.fn();
  const oncancel = vi.fn();
  render(ConfirmDialog, {
    props: {
      open: true,
      message: 'Kill agent "feat-x"? The session and its agent will be terminated.',
      confirmLabel: "Kill",
      onconfirm,
      oncancel,
    },
  });
  await waitFor(() =>
    expect(
      screen.getByText('Kill agent "feat-x"? The session and its agent will be terminated.')
    ).toBeInTheDocument()
  );
});

test("clicking confirm calls onconfirm", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  const onconfirm = vi.fn();
  render(ConfirmDialog, {
    props: { open: true, message: "Are you sure?", confirmLabel: "Kill", onconfirm },
  });
  await waitFor(() => screen.getByRole("button", { name: "Kill" }));
  await fireEvent.click(screen.getByRole("button", { name: "Kill" }));
  expect(onconfirm).toHaveBeenCalledTimes(1);
});

test("clicking cancel calls oncancel", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  const oncancel = vi.fn();
  render(ConfirmDialog, {
    props: { open: true, message: "Are you sure?", oncancel },
  });
  await waitFor(() => screen.getByRole("button", { name: "Cancel" }));
  await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(oncancel).toHaveBeenCalledTimes(1);
});

test("does not render when closed", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  render(ConfirmDialog, {
    props: { open: false, message: "Should not appear" },
  });
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("undo affordance shown for destructive ops", async () => {
  const { default: ConfirmDialog } = await import("./ConfirmDialog.svelte");
  render(ConfirmDialog, { props: {
    open: true, message: "Remove workspace?", destructive: true,
    onConfirm: () => {}, onCancel: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /confirm/i }));
  expect(screen.getByText(/undo/i)).toBeInTheDocument();
});
