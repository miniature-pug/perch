import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("calls onsubmit with tool, path, branch when Create clicked with valid inputs", async () => {
  const { default: NewAgentDialog } = await import("./NewAgentDialog.svelte");
  const onsubmit = vi.fn();
  render(NewAgentDialog, { props: { open: true, onsubmit } });

  await waitFor(() => screen.getByRole("dialog", { name: "new agent" }));

  await fireEvent.input(screen.getByLabelText("project path"), {
    target: { value: "/home/user/myproject" },
  });
  await fireEvent.input(screen.getByLabelText("branch"), {
    target: { value: "feat/my-feature" },
  });
  await fireEvent.click(screen.getByRole("button", { name: "Create" }));

  expect(onsubmit).toHaveBeenCalledWith("claude", "/home/user/myproject", "feat/my-feature");
});

test("calls onsubmit with selected tool when tool changed to opencode", async () => {
  const { default: NewAgentDialog } = await import("./NewAgentDialog.svelte");
  const onsubmit = vi.fn();
  render(NewAgentDialog, { props: { open: true, onsubmit } });

  await waitFor(() => screen.getByRole("dialog", { name: "new agent" }));

  await fireEvent.change(screen.getByLabelText("tool"), {
    target: { value: "opencode" },
  });
  await fireEvent.input(screen.getByLabelText("project path"), {
    target: { value: "/home/user/proj" },
  });
  await fireEvent.input(screen.getByLabelText("branch"), {
    target: { value: "main" },
  });
  await fireEvent.click(screen.getByRole("button", { name: "Create" }));

  expect(onsubmit).toHaveBeenCalledWith("opencode", "/home/user/proj", "main");
});

test("does NOT call onsubmit when fields are empty", async () => {
  const { default: NewAgentDialog } = await import("./NewAgentDialog.svelte");
  const onsubmit = vi.fn();
  render(NewAgentDialog, { props: { open: true, onsubmit } });

  await waitFor(() => screen.getByRole("dialog", { name: "new agent" }));
  await fireEvent.click(screen.getByRole("button", { name: "Create" }));

  expect(onsubmit).not.toHaveBeenCalled();
});

test("calls oncancel when Cancel clicked", async () => {
  const { default: NewAgentDialog } = await import("./NewAgentDialog.svelte");
  const oncancel = vi.fn();
  render(NewAgentDialog, { props: { open: true, oncancel } });

  await waitFor(() => screen.getByRole("dialog", { name: "new agent" }));
  await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

  expect(oncancel).toHaveBeenCalledTimes(1);
});

test("does not render when closed", async () => {
  const { default: NewAgentDialog } = await import("./NewAgentDialog.svelte");
  render(NewAgentDialog, { props: { open: false } });
  expect(screen.queryByRole("dialog")).toBeNull();
});
