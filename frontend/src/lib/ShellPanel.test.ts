// frontend/src/lib/ShellPanel.test.ts
// This file gives jsdom coverage for the shell drawer chrome: the labelled action buttons
// and the reload shell picker. The test stubs ShellDrawer (the per-cell terminal) as an
// inert component. These tests then exercise only ShellPanel's own markup, not xterm or pty.
// Exact visual spacing and tab-strip crowding are manual-smoke-only items.
import { render, screen, waitFor, fireEvent } from "@testing-library/svelte";
import { vi, expect, beforeEach } from "vitest";
import { tick } from "svelte";
import type { ShellPane } from "./shellPanes";

vi.mock("./wails", () => ({ reloadAgentEnv: vi.fn(async () => {}) }));
vi.mock("./ShellDrawer.svelte", async () => ({
  default: (await import("./__stubs__/Empty.svelte")).default,
}));

const noop = () => {};
function baseProps(over: Partial<Record<string, unknown>> = {}) {
  return {
    cwd: "/wt",
    panes: [{ id: "shell-ws1" }] as ShellPane[],
    activeId: "shell-ws1",
    splitId: null,
    collapsed: false,
    onSelect: noop,
    onNew: noop,
    onClose: noop,
    onToggleSplit: vi.fn(),
    onToggleCollapse: vi.fn(),
    ...over,
  };
}

async function renderPanel(over: Partial<Record<string, unknown>> = {}) {
  const { default: ShellPanel } = await import("./ShellPanel.svelte");
  return render(ShellPanel, { props: baseProps(over) });
}

beforeEach(async () => {
  const w = await import("./wails");
  vi.mocked(w.reloadAgentEnv).mockClear();
});

// ── Restored labels ─────────────────────────────────────────────────────────

test("split and collapse actions render their restored text labels", async () => {
  await renderPanel();
  expect(screen.getByRole("button", { name: /split terminals side by side/i })).toHaveTextContent(/Split/);
  expect(screen.getByRole("button", { name: /collapse shell/i })).toHaveTextContent(/Collapse/);
});

test("split button reflects pressed state and label swaps to Expand when collapsed", async () => {
  const { rerender } = await renderPanel({ splitId: null });
  const split = screen.getByRole("button", { name: /split terminals side by side/i });
  expect(split).toHaveAttribute("aria-pressed", "false");

  await rerender(baseProps({ splitId: "shell-ws1_1", panes: [{ id: "shell-ws1" }, { id: "shell-ws1_1" }], collapsed: true }));
  await tick();
  expect(screen.getByRole("button", { name: /split terminals side by side/i })).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("button", { name: /expand shell/i })).toHaveTextContent(/Expand/);
});

// ── Single shell: plain reload button, no picker ─────────────────────────────

test("one shell shows the plain labelled reload button and no picker caret", async () => {
  await renderPanel(); // one pane
  const reload = screen.getByRole("button", { name: /reload agent with this terminal's environment/i });
  expect(reload).toHaveTextContent("↻ env → agent");
  expect(reload).not.toHaveTextContent("·"); // no active-shell suffix when only one
  expect(screen.queryByRole("button", { name: /choose which shell to reload/i })).toBeNull();
  expect(screen.queryByRole("menu")).toBeNull();
});

test("one shell: clicking reload calls reloadAgentEnv with that pane id", async () => {
  await renderPanel();
  const w = await import("./wails");
  await fireEvent.click(screen.getByRole("button", { name: /reload agent with this terminal's environment/i }));
  expect(w.reloadAgentEnv).toHaveBeenCalledWith("shell-ws1");
});

// ── Multiple shells: split-button + picker ───────────────────────────────────

const MULTI = { panes: [{ id: "shell-ws1" }, { id: "shell-ws1_1" }] as ShellPane[], activeId: "shell-ws1_1" };

test("multiple shells: main reload button names the active shell and a caret appears", async () => {
  await renderPanel(MULTI); // active is index 1 -> "shell 2"
  expect(screen.getByRole("button", { name: /reload agent with this terminal's environment/i }))
    .toHaveTextContent(/↻ env → agent · shell 2/);
  expect(screen.getByRole("button", { name: /choose which shell to reload/i })).toBeInTheDocument();
});

test("the picker is closed until the caret is clicked, then lists every shell and marks the active one", async () => {
  await renderPanel(MULTI);
  expect(screen.queryByRole("menu")).toBeNull();

  await fireEvent.click(screen.getByRole("button", { name: /choose which shell to reload/i }));
  await tick();

  await waitFor(() => screen.getByRole("menu"));
  const items = screen.getAllByRole("menuitem");
  expect(items.map((el) => el.textContent?.trim())).toEqual(["shell", "shell 2 (active)"]);
});

test("selecting a specific (non-active) shell calls reloadAgentEnv with THAT pane id and closes the picker", async () => {
  await renderPanel(MULTI); // active shell-ws1_1
  const w = await import("./wails");
  await fireEvent.click(screen.getByRole("button", { name: /choose which shell to reload/i }));
  await tick();

  await fireEvent.click(screen.getByRole("menuitem", { name: /^shell$/ })); // the non-active one
  expect(w.reloadAgentEnv).toHaveBeenCalledWith("shell-ws1"); // not the active shell-ws1_1
  await tick();
  expect(screen.queryByRole("menu")).toBeNull(); // picker closed after choosing
});

test("selecting a shell restores focus to the caret button (not <body>)", async () => {
  await renderPanel(MULTI);
  const caret = screen.getByRole("button", { name: /choose which shell to reload/i });
  await fireEvent.click(caret);
  await tick();
  await waitFor(() => screen.getByRole("menu"));

  // When the user chooses an item, the picker closes. This removes the focused menu item from the DOM.
  // reloadPane must hand focus back to the caret, so focus never lands on <body>.
  await fireEvent.click(screen.getByRole("menuitem", { name: /^shell$/ }));
  await tick();
  expect(document.activeElement).toBe(caret);
});

test("the main split-button still reloads the active shell", async () => {
  await renderPanel(MULTI); // active shell-ws1_1
  const w = await import("./wails");
  await fireEvent.click(screen.getByRole("button", { name: /reload agent with this terminal's environment/i }));
  expect(w.reloadAgentEnv).toHaveBeenCalledWith("shell-ws1_1");
});

test("Escape closes the open picker", async () => {
  await renderPanel(MULTI);
  await fireEvent.click(screen.getByRole("button", { name: /choose which shell to reload/i }));
  await waitFor(() => screen.getByRole("menu"));
  // The key handler lives on the focusable menu items. Focus lands on one item when the
  // picker opens. This mirrors the MenuBar dropdown convention.
  await fireEvent.keyDown(screen.getByRole("menuitem", { name: /active/i }), { key: "Escape" });
  await tick();
  expect(screen.queryByRole("menu")).toBeNull();
});
