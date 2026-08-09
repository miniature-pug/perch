// frontend/src/lib/ShellDrawer.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi, expect } from "vitest";
import { tick } from "svelte";

vi.mock("./wails", () => ({ openShell: vi.fn(async () => {}), reloadAgentEnv: vi.fn(async () => {}) }));
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

// ── Reload-agent-env button ─────────────────────────────────────────────────
// Per docs/superpowers/specs/2026-08-09-perch-session-env-reload-design.md §7:
// present only on a per-workspace drawer, absent on the home drawer.

test("reload button renders on a per-workspace drawer", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-ws1", cwd: "/wt" } });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /reload agent with this terminal's environment/i })).toBeInTheDocument(),
  );
});

test("reload button is absent on the home drawer", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-home", cwd: "/home" } });
  await waitFor(() => screen.getByRole("region", { name: /shell/i }));
  expect(screen.queryByRole("button", { name: /reload agent with this terminal's environment/i })).toBeNull();
});

test("clicking the reload button calls reloadAgentEnv with the drawer's paneId", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-ws1", cwd: "/wt" } });
  const w = await import("./wails");
  const btn = await waitFor(() => screen.getByRole("button", { name: /reload agent with this terminal's environment/i }));
  await fireEvent.click(btn);
  expect(w.reloadAgentEnv).toHaveBeenCalledWith("shell-ws1");
});

test("reload button carries the file-credential hint text", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-ws1", cwd: "/wt" } });
  const btn = await waitFor(() => screen.getByRole("button", { name: /reload agent with this terminal's environment/i }));
  expect(btn.getAttribute("title")).toMatch(/AWS SSO/i);
  expect(btn.getAttribute("title")).toMatch(/no reload/i);
  // getByTitle is the accessible route to the same hint, confirming it renders as a real tooltip.
  expect(screen.getByTitle(/no reload/i)).toBe(btn);
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
