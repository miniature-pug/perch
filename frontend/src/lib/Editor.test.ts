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
  // Seed a known content value so we can assert the exact string is round-tripped.
  vi.mocked(w.readFile).mockResolvedValueOnce("KNOWN_SAVE_CONTENT");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  // Wait for readFile to be called and content to be loaded into the editor.
  await waitFor(() => expect(w.readFile).toHaveBeenCalledWith("/wt/src/main.go"));
  await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
  // Assert writeFile receives the exact content seeded by readFile — proves round-trip propagation.
  await waitFor(() => expect(w.writeFile).toHaveBeenCalledWith("/wt/src/main.go", "KNOWN_SAVE_CONTENT"));
});

test("renders nothing when path is null", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: null, worktree: "/wt" } });
  expect(screen.queryByRole("region", { name: "editor" })).toBeNull();
});

test("mounts the git change gutter for a changed file", async () => {
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("line1\nline2\nline3\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([{
    file: "/wt/src/main.go", index: 0, header: "@@ -1,1 +1,2 @@",
    oldStart: 1, oldLines: 1, newStart: 1, newLines: 2,
    lines: [{ kind: "add", text: "line1a" }, { kind: "ctx", text: "line2" }],
  }]);
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  // CodeMirror does not lay out individual gutter line markers in jsdom (zero-size
  // viewport). The changed-line computation is unit-tested in gutter.test.ts; here
  // we assert the gutter extension itself is mounted on a changed file.
  await waitFor(() =>
    expect(document.querySelector(".perch-git-gutter")).not.toBeNull()
  );
});
