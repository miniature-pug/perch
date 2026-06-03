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
