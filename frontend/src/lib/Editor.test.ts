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

// --- search extension ---

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

// --- send-to-agent affordance ---

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

// --- dirty/unsaved indicator ---

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

// --- send-to-agent button drag affordance ---

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

// --- external change reload (reloadToken), preserving unsaved drafts ---

test("external change reloads a clean editor when reloadToken bumps", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValue("v1");
  vi.mocked(w.hunks).mockResolvedValue([]);
  const { rerender } = render(Editor, {
    props: { path: "/wt/a.ts", worktree: "/wt", reloadToken: 0 },
  });
  await waitFor(() => expect(w.readFile).toHaveBeenCalledWith("/wt/a.ts"));
  const callsAfterMount = vi.mocked(w.readFile).mock.calls.length;
  // A file changed on disk; the parent bumps reloadToken. A clean editor reloads.
  await rerender({ path: "/wt/a.ts", worktree: "/wt", reloadToken: 1 });
  await waitFor(() =>
    expect(vi.mocked(w.readFile).mock.calls.length).toBeGreaterThan(callsAfterMount)
  );
});

test("external change does NOT reload a dirty editor, so the draft survives", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValue("original\n");
  vi.mocked(w.hunks).mockResolvedValue([]);
  const { rerender } = render(Editor, {
    props: { path: "/wt/a.ts", worktree: "/wt", reloadToken: 0 },
  });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  const view = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  // Type an unsaved edit: the editor is now dirty.
  view.dispatch({ changes: { from: 0, to: 0, insert: "DRAFT " } });
  await waitFor(() => expect(document.querySelector(".dirty-dot")).not.toBeNull());
  const callsBefore = vi.mocked(w.readFile).mock.calls.length;
  // An external change arrives while dirty. The editor must NOT reload (that would
  // discard the draft): no new readFile, still dirty, draft text intact.
  await rerender({ path: "/wt/a.ts", worktree: "/wt", reloadToken: 1 });
  await new Promise((r) => setTimeout(r, 30));
  expect(vi.mocked(w.readFile).mock.calls.length).toBe(callsBefore);
  expect(document.querySelector(".dirty-dot")).not.toBeNull();
  expect(view.state.doc.toString()).toContain("DRAFT ");
});

// --- F14b: unmounting a dirty editor must not silently drop the unsaved draft ---

test("unmounting a dirty editor persists the draft instead of silently dropping it", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValue("original\n");
  vi.mocked(w.hunks).mockResolvedValue([]);
  vi.mocked(w.writeFile).mockClear();

  const { unmount } = render(Editor, { props: { path: "/wt/a.ts", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  const view = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  // An unsaved edit: the buffer is now dirty.
  view.dispatch({ changes: { from: 0, to: 0, insert: "DRAFT " } });
  await waitFor(() => expect(document.querySelector(".dirty-dot")).not.toBeNull());

  // A session switch destroys the editor. onDestroy must save the dirty buffer
  // (capturing the current document) rather than discarding it with view.destroy().
  unmount();
  await waitFor(() =>
    expect(w.writeFile).toHaveBeenCalledWith("/wt/a.ts", "DRAFT original\n")
  );
});

// --- F7b: a same-file reload preserves the caret/selection (not reset to 0) ---

test("selection survives a same-file reloadToken 1->0->1 oscillation", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const w = await import("./wails");
  // Identical clean content on every reload, long enough to hold an offset-40 caret.
  vi.mocked(w.readFile).mockResolvedValue("0123456789\n".repeat(20));
  vi.mocked(w.hunks).mockResolvedValue([]);

  const { rerender } = render(Editor, {
    props: { path: "/wt/a.ts", worktree: "/wt", reloadToken: 1 },
  });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  const view = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  // Place the caret at offset 40. A selection-only dispatch must not mark dirty, so
  // the same-file reload below still proceeds.
  view.dispatch({ selection: EditorSelection.single(40) });
  expect(view.state.selection.main.head).toBe(40);
  expect(document.querySelector(".dirty-dot")).toBeNull();

  // Oscillate reloadToken 1 -> 0 -> 1: same clean file, unchanged content. Before the
  // fix each reload rebuilt state via setState and reset the caret to 0.
  await rerender({ path: "/wt/a.ts", worktree: "/wt", reloadToken: 0 });
  await new Promise((r) => setTimeout(r, 20));
  await rerender({ path: "/wt/a.ts", worktree: "/wt", reloadToken: 1 });
  await new Promise((r) => setTimeout(r, 20));

  const after = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  expect(after.state.selection.main.head).toBe(40);
});

// --- F9: an out-of-order readFile resolve must not show a stale file ---

test("readFile(A) resolving after readFile(B) leaves the editor showing B", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const w = await import("./wails");
  vi.mocked(w.hunks).mockResolvedValue([]);

  // Gate readFile("/wt/a.ts") so it resolves AFTER readFile("/wt/b.ts").
  let releaseA: () => void = () => {};
  const aPending = new Promise<string>((resolve) => {
    releaseA = () => resolve("A-content");
  });
  vi.mocked(w.readFile).mockImplementation(async (p: string) => {
    if (p === "/wt/a.ts") return aPending;
    if (p === "/wt/b.ts") return "B-content";
    return "";
  });

  const { rerender } = render(Editor, {
    props: { path: "/wt/a.ts", worktree: "/wt", reloadToken: 0 },
  });
  // A is in-flight (gated). Switch to B before A resolves.
  await rerender({ path: "/wt/b.ts", worktree: "/wt", reloadToken: 0 });
  // B resolves first and mounts with B-content.
  await waitFor(() => {
    const cm = document.querySelector(".cm-editor");
    expect(cm).not.toBeNull();
    const v = EditorView.findFromDOM(cm as HTMLElement);
    expect(v?.state.doc.toString()).toBe("B-content");
  });

  // Now let the stale A resolve — its generation is superseded, so it must be dropped.
  releaseA();
  await new Promise((r) => setTimeout(r, 30));
  const v = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  expect(v.state.doc.toString()).toBe("B-content");
});
