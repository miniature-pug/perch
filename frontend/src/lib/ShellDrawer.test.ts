// frontend/src/lib/ShellDrawer.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

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

test("collapse toggle hides the shell region", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-1", cwd: "/wt" } });
  await waitFor(() => screen.getByRole("button", { name: /collapse/i }));
  expect(screen.getByRole("region", { name: /shell/i })).toBeInTheDocument();
  await fireEvent.click(screen.getByRole("button", { name: /collapse/i }));
  expect(screen.queryByRole("region", { name: /shell/i })).toBeNull();
  await fireEvent.click(screen.getByRole("button", { name: /expand/i }));
  await waitFor(() => expect(screen.getByRole("region", { name: /shell/i })).toBeInTheDocument());
});
