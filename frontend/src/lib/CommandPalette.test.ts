// frontend/src/lib/CommandPalette.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const commands = [
  { id: "agent:new",  group: "Agent", label: "New agent session",  keybinding: "Ctrl-N" },
  { id: "agent:kill", group: "Agent", label: "Kill agent",          keybinding: "" },
  { id: "file:open",  group: "File",  label: "Open file",           keybinding: "Ctrl-O" },
  { id: "view:split", group: "View",  label: "Split pane",          keybinding: "\\" },
  { id: "pane:focus", group: "Pane",  label: "Focus terminal pane", keybinding: "i" },
];

test("fuzzy match: 'kil' surfaces Kill agent first", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose: () => {} } });
  await fireEvent.input(screen.getByRole("combobox"), { target: { value: "kil" } });
  const items = screen.getAllByRole("option");
  expect(items[0].textContent).toMatch(/kill agent/i);
});

test("Enter runs the top result and calls onRun", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  const onRun = vi.fn();
  render(CommandPalette, { props: { open: true, commands, onRun, onClose: () => {} } });
  await fireEvent.input(screen.getByRole("combobox"), { target: { value: "new" } });
  await fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter" });
  expect(onRun).toHaveBeenCalledWith("agent:new");
});

test("Esc calls onClose", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  const onClose = vi.fn();
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose } });
  await fireEvent.keyDown(screen.getByRole("combobox"), { key: "Escape" });
  expect(onClose).toHaveBeenCalled();
});

test("prefix group labels and inline keybindings are shown", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose: () => {} } });
  expect(screen.getByText("Agent:")).toBeInTheDocument();
  expect(screen.getByText("Ctrl-N")).toBeInTheDocument();
});

test("ArrowDown then Enter runs the SECOND command (active-descendant navigation)", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  const onRun = vi.fn();
  render(CommandPalette, { props: { open: true, commands, onRun, onClose: () => {} } });
  const input = screen.getByRole("combobox");
  // Without a query all commands are shown; ArrowDown moves active from 0 to 1
  await fireEvent.keyDown(input, { key: "ArrowDown" });
  await fireEvent.keyDown(input, { key: "Enter" });
  // The second command in the unfiltered list is agent:kill
  expect(onRun).toHaveBeenCalledWith("agent:kill");
});

test("active index resets to 0 when query changes", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  const onRun = vi.fn();
  render(CommandPalette, { props: { open: true, commands, onRun, onClose: () => {} } });
  const input = screen.getByRole("combobox");
  // Move down twice, then change query — active must reset so Enter hits filtered[0]
  await fireEvent.keyDown(input, { key: "ArrowDown" });
  await fireEvent.keyDown(input, { key: "ArrowDown" });
  await fireEvent.input(input, { target: { value: "kil" } });
  await fireEvent.keyDown(input, { key: "Enter" });
  expect(onRun).toHaveBeenCalledWith("agent:kill");
});

test("active option gets aria-selected=true; others get aria-selected=false", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose: () => {} } });
  const input = screen.getByRole("combobox");
  // Initially first option is active
  const options = screen.getAllByRole("option");
  expect(options[0].getAttribute("aria-selected")).toBe("true");
  expect(options[1].getAttribute("aria-selected")).toBe("false");
  // After ArrowDown, second option is active
  await fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(screen.getAllByRole("option")[0].getAttribute("aria-selected")).toBe("false");
  expect(screen.getAllByRole("option")[1].getAttribute("aria-selected")).toBe("true");
});

test("aria-activedescendant on input points to active option id", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  render(CommandPalette, { props: { open: true, commands, onRun: () => {}, onClose: () => {} } });
  const input = screen.getByRole("combobox");
  const options = screen.getAllByRole("option");
  // Initially points to first option
  expect(input.getAttribute("aria-activedescendant")).toBe(options[0].id);
  // After ArrowDown, points to second option
  await fireEvent.keyDown(input, { key: "ArrowDown" });
  expect(input.getAttribute("aria-activedescendant")).toBe(screen.getAllByRole("option")[1].id);
});
