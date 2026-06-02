import { render, screen, fireEvent } from "@testing-library/svelte";

test("filters commands and runs the chosen one", async () => {
  const { default: CommandPalette } = await import("./CommandPalette.svelte");
  let ran = "";
  const commands = [
    { id: "new", label: "New agent", run: () => (ran = "new") },
    { id: "kill", label: "Kill agent", run: () => (ran = "kill") },
  ];
  render(CommandPalette, { props: { open: true, commands } });
  await fireEvent.input(screen.getByRole("textbox"), { target: { value: "kill" } });
  await fireEvent.click(screen.getByText("Kill agent"));
  expect(ran).toBe("kill");
});
