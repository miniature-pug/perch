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

// --- Feature 1: search extension ---

test("search extension is active: openSearchPanel renders .cm-search panel", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { openSearchPanel } = await import("@codemirror/search");
  const { EditorView } = await import("@codemirror/view");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("hello world\nfoo bar\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  // Wait for the CM editor to mount
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());

  // Retrieve the live EditorView from the DOM
  const cmEditor = document.querySelector(".cm-editor") as HTMLElement;
  const editorView = EditorView.findFromDOM(cmEditor);
  expect(editorView).not.toBeNull();

  // Open the search panel programmatically
  openSearchPanel(editorView!);

  // The search panel should appear in the DOM
  await waitFor(() =>
    expect(document.querySelector(".cm-search")).not.toBeNull(),
    { timeout: 2000 }
  );
});

// --- Feature 3: send-to-agent affordance ---

test("send-to-agent button is hidden when no selection", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const spy = vi.fn();
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt", onSendToAgent: spy } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  // No selection yet — button must not appear
  expect(screen.queryByRole("button", { name: /send to agent/i })).toBeNull();
});

test("send-to-agent button appears after selection and calls spy with selected text", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const spy = vi.fn();
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("hello world\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt", onSendToAgent: spy } });

  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());

  const cmEditor = document.querySelector(".cm-editor") as HTMLElement;
  const editorView = EditorView.findFromDOM(cmEditor);
  expect(editorView).not.toBeNull();

  // Programmatically select "hello world" (chars 0-11)
  editorView!.dispatch({
    selection: EditorSelection.single(0, 11),
  });

  // Button should appear
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /send to agent/i })).toBeInTheDocument()
  );

  // Click the button
  await fireEvent.click(screen.getByRole("button", { name: /send to agent/i }));

  // Spy should have been called with the selected text
  expect(spy).toHaveBeenCalledWith("hello world");
});

test("send-to-agent button does not render when onSendToAgent prop is absent", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("hello world\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);

  // No onSendToAgent prop provided
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });

  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());

  const cmEditor = document.querySelector(".cm-editor") as HTMLElement;
  const editorView = EditorView.findFromDOM(cmEditor);
  editorView!.dispatch({ selection: EditorSelection.single(0, 5) });

  // Even with a selection, the button should not appear without the prop
  await new Promise((r) => setTimeout(r, 50));
  expect(screen.queryByRole("button", { name: /send to agent/i })).toBeNull();
});

// --- N-10: dirty/unsaved indicator ---

test("dirty dot is absent immediately after load (file is clean)", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("some content\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  // Allow any async microtasks to flush
  await new Promise((r) => setTimeout(r, 20));
  expect(document.querySelector(".dirty-dot")).toBeNull();
});

test("dirty dot appears after editing the document", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("original\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());

  const cmEditor = document.querySelector(".cm-editor") as HTMLElement;
  const editorView = EditorView.findFromDOM(cmEditor);
  expect(editorView).not.toBeNull();

  // Insert a character to make the document dirty
  editorView!.dispatch({
    changes: { from: 0, to: 0, insert: "X" },
  });

  await waitFor(() =>
    expect(document.querySelector(".dirty-dot")).not.toBeNull()
  );
});

test("dirty dot disappears after Ctrl-S save", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("original\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());

  const cmEditor = document.querySelector(".cm-editor") as HTMLElement;
  const editorView = EditorView.findFromDOM(cmEditor);
  // Make dirty
  editorView!.dispatch({ changes: { from: 0, to: 0, insert: "Y" } });
  await waitFor(() => expect(document.querySelector(".dirty-dot")).not.toBeNull());

  // Save
  await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
  await waitFor(() => expect(document.querySelector(".dirty-dot")).toBeNull());
});

// --- N-24: send-to-agent button drag affordance ---

test("send-to-agent button is draggable and sets perch text MIME on dragstart", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("hello world\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);
  const spy = vi.fn();

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt", onSendToAgent: spy } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());

  const cmEditor = document.querySelector(".cm-editor") as HTMLElement;
  const editorView = EditorView.findFromDOM(cmEditor);
  // Select "hello world"
  editorView!.dispatch({ selection: EditorSelection.single(0, 11) });

  const btn = await waitFor(() => screen.getByRole("button", { name: /send to agent/i }));

  // Verify draggable attribute
  expect(btn).toHaveAttribute("draggable", "true");

  // Simulate dragstart with a mock dataTransfer
  const mockDataTransfer: Partial<DataTransfer> = {
    effectAllowed: "none" as DataTransfer["effectAllowed"],
    items: [] as unknown as DataTransferItemList,
    setData: vi.fn(),
  };
  await fireEvent.dragStart(btn, { dataTransfer: mockDataTransfer });

  expect(mockDataTransfer.setData).toHaveBeenCalledWith(
    "application/x-perch-text",
    "hello world"
  );
});
