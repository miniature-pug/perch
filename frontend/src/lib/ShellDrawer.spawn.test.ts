// frontend/src/lib/ShellDrawer.spawn.test.ts
// FEX-15 / FEX-33: the drawer re-sends the terminal size once the shell
// exists, and reports a failed spawn or env reload instead of failing
// silently.
import { render, screen, waitFor, fireEvent } from "@testing-library/svelte";
import { vi, expect, beforeEach, test } from "vitest";
import { terminalCalls } from "./__stubs__/terminalCalls";

vi.mock("./wails", () => ({ openShell: vi.fn(async () => {}), reloadAgentEnv: vi.fn(async () => {}) }));
vi.mock("./Terminal.svelte", async () => ({
  default: (await import("./__stubs__/TerminalNoticeProbe.svelte")).default,
}));

beforeEach(() => { terminalCalls.length = 0; });

test("FEX-15: resyncs the terminal size after openShell resolves", async () => {
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-a", cwd: "/wt" } });
  await waitFor(() => expect(terminalCalls).toContainEqual({ paneId: "shell-a", call: "resync" }));
});

test("FEX-33: a failed openShell writes a notice into the terminal", async () => {
  const w = await import("./wails");
  vi.mocked(w.openShell).mockRejectedValueOnce(new Error("no such directory"));
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-b", cwd: "/gone" } });
  await waitFor(() => expect(terminalCalls.some((c) => c.call === "notice" && /no such directory/.test(c.text ?? ""))).toBe(true));
});

test("FEX-33: a failed env reload raises a notification", async () => {
  const w = await import("./wails");
  const { getItems } = await import("./stores/notifications.svelte");
  vi.mocked(w.reloadAgentEnv).mockRejectedValueOnce(new Error("unknown pane"));
  const { default: ShellDrawer } = await import("./ShellDrawer.svelte");
  render(ShellDrawer, { props: { paneId: "shell-ws9", cwd: "/wt" } });
  await fireEvent.click(await screen.findByRole("button", { name: /reload agent/i }));
  await waitFor(() => expect(getItems().some((n) => n.title === "Could not reload the agent")).toBe(true));
});
