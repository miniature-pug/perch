// frontend/src/lib/Editor.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({
  readFile: vi.fn(async () => "initial content"),
  writeFile: vi.fn(async () => {}),
  hunks: vi.fn(async () => []),
}));

test("loads file content on mount", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(screen.getByRole("region", { name: "editor" })).toBeInTheDocument());
  const w = await import("./wails");
  expect(w.readFile).toHaveBeenCalledWith("/wt/src/main.go");
});

test("saves via Ctrl-S", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const w = await import("./wails");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(w.readFile).toHaveBeenCalled());
  await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
  await waitFor(() => expect(w.writeFile).toHaveBeenCalledWith("/wt/src/main.go", expect.any(String)));
});

test("renders nothing when path is null", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: null, worktree: "/wt" } });
  expect(screen.queryByRole("region", { name: "editor" })).toBeNull();
});
