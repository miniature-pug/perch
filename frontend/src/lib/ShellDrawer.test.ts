// frontend/src/lib/ShellDrawer.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi, expect } from "vitest";
import { tick } from "svelte";

vi.mock("./wails", () => ({ openShell: vi.fn(async () => {}) }));
vi.mock("./Terminal.svelte", async () => ({
  default: (await import("./__stubs__/Empty.svelte")).default,
}));

test("calls openShell on mount", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-1", cwd: "/wt" } });
  const w = await import("./wails");
  await waitFor(() => expect(w.openShell).toHaveBeenCalledWith("shell-1", "/wt"));
});

// collapsed is a prop; onToggleCollapse is called when the button is clicked.
test("collapse toggle calls onToggleCollapse when button clicked", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  const onToggleCollapse = vi.fn();
  render(ShellDrawer, { props: { paneId: "shell-1", cwd: "/wt", collapsed: false, onToggleCollapse } });
  await waitFor(() => screen.getByRole("button", { name: /collapse/i }));
  expect(screen.getByRole("region", { name: /shell/i })).toBeInTheDocument();

  await fireEvent.click(screen.getByRole("button", { name: /collapse/i }));
  await tick();
  // onToggleCollapse must have been called — App.svelte is the one that changes collapsed
  expect(onToggleCollapse).toHaveBeenCalledTimes(1);
});

test("collapsed=true prop hides the shell region; collapsed=false shows it", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  // Start expanded
  const { rerender } = render(ShellDrawer, { props: { paneId: "shell-1", cwd: "/wt", collapsed: false } });
  expect(screen.getByRole("region", { name: /shell/i })).toBeInTheDocument();

  // Switch to collapsed via prop
  await rerender({ props: { paneId: "shell-1", cwd: "/wt", collapsed: true } });
  await tick();
  expect(screen.queryByRole("region", { name: /shell/i })).toBeNull();
  expect(screen.getByRole("button", { name: /expand/i })).toBeInTheDocument();

  // Switch back to expanded
  await rerender({ props: { paneId: "shell-1", cwd: "/wt", collapsed: false } });
  await tick();
  await waitFor(() => expect(screen.getByRole("region", { name: /shell/i })).toBeInTheDocument());
});
