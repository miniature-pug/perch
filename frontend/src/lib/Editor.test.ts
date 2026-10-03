// frontend/src/lib/Editor.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./wails", () => ({
  readFile: vi.fn(async () => "initial content"),
  writeFile: vi.fn(async () => {}),
  hunks: vi.fn(async () => []),
  clipboardSetText: vi.fn(async () => {}),
  clipboardText: vi.fn(async () => ""),
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
  // This seeds a known content value, so the test can assert the exact string round-trips.
  vi.mocked(w.readFile).mockResolvedValueOnce("KNOWN_SAVE_CONTENT");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  // Wait for readFile to run and the editor to load the content.
  await waitFor(() => expect(w.readFile).toHaveBeenCalledWith("/wt/src/main.go"));
  await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
  // This asserts writeFile receives the exact content seeded by readFile. This proves round-trip propagation.
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
    file: "/wt/src/main.go", index: 0, id: "h0", header: "@@ -1,1 +1,2 @@",
    oldStart: 1, oldLines: 1, newStart: 1, newLines: 2,
    lines: [{ kind: "add", text: "line1a" }, { kind: "ctx", text: "line2" }],
  }]);
  const { default: Editor } = await import("./Editor.svelte");
  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  // CodeMirror does not lay out individual gutter line markers in jsdom (zero-size
  // viewport). gutter.test.ts unit-tests the changed-line computation separately. Here,
  // this test asserts only that the gutter extension mounts on a changed file.
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
  // No selection yet. The button must not appear.
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

  // The spy should have received the selected text.
  expect(spy).toHaveBeenCalledWith("hello world");
});

test("send-to-agent button does not render when onSendToAgent prop is absent", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("hello world\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);

  // The test provides no onSendToAgent prop.
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
  // Make the document dirty.
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
  // A file changed on disk. The parent bumps reloadToken. A clean editor then reloads.
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
  // An external change arrives while the editor is dirty. The editor must NOT reload,
  // because that would discard the draft. It calls no new readFile, stays dirty, and keeps the draft text intact.
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

  // A session switch destroys the editor. onDestroy must save the dirty buffer by
  // capturing the current document, rather than discarding the buffer with view.destroy().
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

  // Oscillate reloadToken from 1 to 0 to 1: same clean file, unchanged content. Before the
  // fix, each reload rebuilt state via setState and reset the caret to 0.
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

  // Now let the stale A resolve. Its generation is superseded, so the editor must drop it.
  releaseA();
  await new Promise((r) => setTimeout(r, 30));
  const v = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  expect(v.state.doc.toString()).toBe("B-content");
});

// --- B4: WebKit2GTK clipboard keymap (Ctrl-Shift-C copy / Ctrl-Shift-V paste) ---
// CodeMirror leaves copy, cut, and paste to the browser's native clipboard. This
// clipboard is unreliable under WebKit2GTK, so the keymap routes both actions through the host binding.

test("B4: Ctrl-Shift-C copies the CM selection via clipboardSetText", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("hello world\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);
  vi.mocked(w.clipboardSetText).mockClear();

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  const view = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;

  // Select "hello world" (chars 0-11).
  view.dispatch({ selection: EditorSelection.single(0, 11) });

  // Ctrl-Shift-C on the editor content routes the selection to the host clipboard.
  await fireEvent.keyDown(view.contentDOM, {
    key: "c", code: "KeyC", keyCode: 67, ctrlKey: true, shiftKey: true,
  });
  await waitFor(() => expect(w.clipboardSetText).toHaveBeenCalledWith("hello world"));
});

test("B4: Ctrl-Shift-C with an empty selection does not copy (falls through)", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("hello world\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);
  vi.mocked(w.clipboardSetText).mockClear();

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  const view = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  // Collapse the selection to a bare caret (from === to).
  view.dispatch({ selection: EditorSelection.single(3) });

  await fireEvent.keyDown(view.contentDOM, {
    key: "c", code: "KeyC", keyCode: 67, ctrlKey: true, shiftKey: true,
  });
  await new Promise((r) => setTimeout(r, 20));
  expect(w.clipboardSetText).not.toHaveBeenCalled();
});

test("B4: Ctrl-Shift-V pastes clipboardText() over the selection via a CM transaction", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { EditorSelection } = await import("@codemirror/state");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValueOnce("AB\n");
  vi.mocked(w.hunks).mockResolvedValueOnce([]);
  vi.mocked(w.clipboardText).mockResolvedValue("PASTED");

  render(Editor, { props: { path: "/wt/src/main.go", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  const view = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  // Caret between A and B (offset 1).
  view.dispatch({ selection: EditorSelection.single(1) });

  await fireEvent.keyDown(view.contentDOM, {
    key: "v", code: "KeyV", keyCode: 86, ctrlKey: true, shiftKey: true,
  });
  // The paste dispatch runs after the async clipboardText() resolves.
  await waitFor(() => expect(view.state.doc.toString()).toBe("APASTEDB\n"));
});

// --- Audit regressions: file switches, failed loads, and saves (FEX-1/2/3/12/16/17/29/30/31) ---

describe("audit: Editor switch and save safety", () => {
  async function setup(initial: string | null = "/wt/a.ts") {
    const w = await import("./wails");
    const { flushSync } = await import("svelte");
    const { EditorView } = await import("@codemirror/view");
    const { default: Host } = await import("./__stubs__/EditorHost.svelte");
    vi.mocked(w.readFile).mockReset();
    vi.mocked(w.readFile).mockImplementation(async (p: string) => "content of " + p);
    vi.mocked(w.writeFile).mockReset();
    vi.mocked(w.writeFile).mockImplementation(async () => {});
    vi.mocked(w.hunks).mockReset();
    vi.mocked(w.hunks).mockImplementation(async () => []);
    const r = render(Host, { props: { path: initial } });
    const cmView = () => {
      const el = document.querySelector(".cm-editor") as HTMLElement | null;
      return el ? EditorView.findFromDOM(el) : null;
    };
    const shown = () => cmView()?.state.doc.toString() ?? null;
    const type = (text: string) => {
      const v = cmView()!;
      v.dispatch({ changes: { from: 0, insert: text } });
    };
    const setPath = (p: string | null) => { r.component.setPath(p); flushSync(); };
    const bump = () => { r.component.bump(); flushSync(); };
    if (initial) await waitFor(() => expect(shown()).toBe("content of " + initial));
    return { w, r, shown, type, setPath, bump, cmView };
  }

  test("FEX-1: a dirty buffer is saved to its own file, never into the previewed file", async () => {
    const { w, type, setPath } = await setup();
    type("EDIT ");
    setPath("/wt/readme.md");
    await waitFor(() => expect(w.writeFile).toHaveBeenCalled());
    expect(vi.mocked(w.writeFile).mock.calls).toEqual([["/wt/a.ts", "EDIT content of /wt/a.ts"]]);
  });

  test("FEX-2: switching away from a dirty file saves it before the next file loads", async () => {
    const { w, type, setPath, shown } = await setup();
    type("EDIT ");
    setPath("/wt/b.ts");
    await waitFor(() => expect(shown()).toBe("content of /wt/b.ts"));
    expect(vi.mocked(w.writeFile).mock.calls).toEqual([["/wt/a.ts", "EDIT content of /wt/a.ts"]]);
    expect(document.querySelector(".dirty-dot")).toBeNull();
  });

  test("FEX-2: a failed save on switch keeps the old file open and dirty, writes nothing else", async () => {
    const { w, type, setPath, shown } = await setup();
    vi.mocked(w.writeFile).mockRejectedValueOnce(new Error("disk full"));
    type("EDIT ");
    setPath("/wt/b.ts");
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/a\.ts could not be saved/));
    expect(shown()).toBe("EDIT content of /wt/a.ts");
    expect(document.querySelector(".dirty-dot")).not.toBeNull();
    // A retry with Ctrl-S writes a.ts to a.ts, then the switch to b.ts resumes.
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    await waitFor(() => expect(shown()).toBe("content of /wt/b.ts"));
    const calls = vi.mocked(w.writeFile).mock.calls;
    expect(calls.every(([p]) => p === "/wt/a.ts")).toBe(true);
  });

  test("FEX-3: a failed load shows an error and Ctrl-S cannot write the old text into the new file", async () => {
    const { w, setPath, shown } = await setup();
    vi.mocked(w.readFile).mockRejectedValueOnce(new Error("file too large"));
    setPath("/wt/big.json");
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/Could not open big\.json: file too large/));
    expect(shown()).toBeNull();
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    await new Promise((r) => setTimeout(r, 20));
    expect(w.writeFile).not.toHaveBeenCalled();
  });

  test("FEX-17: a reload bump during a switch still loads the new file (old buffer never lands in it)", async () => {
    const { w, type, setPath, bump, shown } = await setup();
    type("EDIT ");
    let releaseB: (s: string) => void = () => {};
    vi.mocked(w.readFile).mockImplementation((p: string) =>
      p === "/wt/b.ts" ? new Promise<string>((res) => { releaseB = res; }) : Promise.resolve("content of " + p));
    setPath("/wt/b.ts");
    await waitFor(() => expect(w.readFile).toHaveBeenCalledWith("/wt/b.ts"));
    bump();
    releaseB("content of /wt/b.ts");
    await waitFor(() => expect(shown()).toBe("content of /wt/b.ts"));
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    await new Promise((r) => setTimeout(r, 20));
    const calls = vi.mocked(w.writeFile).mock.calls;
    expect(calls).toContainEqual(["/wt/a.ts", "EDIT content of /wt/a.ts"]);
    expect(calls.filter(([p]) => p === "/wt/b.ts").every(([, c]) => c === "content of /wt/b.ts")).toBe(true);
  });

  test("FEX-16: keystrokes typed during an in-flight save survive the save and the reload it triggers", async () => {
    const { w, type, bump, shown } = await setup();
    type("ONE ");
    let releaseWrite: () => void = () => {};
    vi.mocked(w.writeFile).mockImplementationOnce(() => new Promise<void>((res) => { releaseWrite = res; }));
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    type("TWO ");
    releaseWrite();
    await new Promise((r) => setTimeout(r, 5));
    // The watcher reports the save's own write.
    vi.mocked(w.readFile).mockResolvedValueOnce("ONE content of /wt/a.ts");
    bump();
    await new Promise((r) => setTimeout(r, 20));
    expect(shown()).toBe("TWO ONE content of /wt/a.ts");
    expect(document.querySelector(".dirty-dot")).not.toBeNull();
  });

  test("FEX-31: path a -> null -> b re-renders the editor (no detached view)", async () => {
    const { setPath, shown } = await setup();
    setPath(null);
    await new Promise((r) => setTimeout(r, 10));
    setPath("/wt/b.ts");
    await waitFor(() => expect(shown()).toBe("content of /wt/b.ts"));
    expect(document.querySelector(".cm-host .cm-editor")).not.toBeNull();
  });

  test("FEX-31: a dirty buffer is saved when the path goes null", async () => {
    const { w, type, setPath } = await setup();
    type("EDIT ");
    setPath(null);
    await waitFor(() => expect(w.writeFile).toHaveBeenCalledWith("/wt/a.ts", "EDIT content of /wt/a.ts"));
  });

  test("FEX-31: a failed save when the path goes null keeps the edits for when the file returns", async () => {
    const { w, type, setPath, shown } = await setup();
    vi.mocked(w.writeFile).mockRejectedValueOnce(new Error("disk full"));
    type("EDIT ");
    setPath(null);
    await waitFor(() => expect(w.writeFile).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 10));
    setPath("/wt/a.ts");
    await waitFor(() => expect(shown()).toBe("EDIT content of /wt/a.ts"));
    expect(document.querySelector(".dirty-dot")).not.toBeNull();
  });

  test("FEX-12: the git gutter asks for hunks with the worktree-relative path", async () => {
    const { w } = await setup("/wt/src/main.go");
    await waitFor(() => expect(w.hunks).toHaveBeenCalledWith("/wt", "src/main.go"));
  });

  test("FEX-29: Ctrl-S with Caps Lock (key 'S') still saves", async () => {
    const { w, type } = await setup();
    type("X");
    await fireEvent.keyDown(document, { key: "S", ctrlKey: true });
    await waitFor(() => expect(w.writeFile).toHaveBeenCalledWith("/wt/a.ts", "Xcontent of /wt/a.ts"));
  });

  test("FEC-24: a failed save notifies under the owning session, not the file path", async () => {
    const { w, type } = await setup();
    const { getItems } = await import("./stores/notifications.svelte");
    vi.mocked(w.writeFile).mockRejectedValueOnce(new Error("EACCES"));
    type("X");
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    await waitFor(() => expect(getItems().some((n) => n.title === "Save failed")).toBe(true));
    const n = getItems().find((n) => n.title === "Save failed")!;
    expect(n.workspaceId).toBe("ws-1");
    expect(n.body).toContain("/wt/a.ts");
  });
});

test("FEX-30: an unknown file type gets no JavaScript language support", async () => {
  const { default: Editor } = await import("./Editor.svelte");
  const { EditorView } = await import("@codemirror/view");
  const { language } = await import("@codemirror/language");
  const w = await import("./wails");
  vi.mocked(w.readFile).mockResolvedValue("key: value\n");
  render(Editor, { props: { path: "/wt/config.yaml", worktree: "/wt" } });
  await waitFor(() => expect(document.querySelector(".cm-editor")).not.toBeNull());
  const view = EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement)!;
  expect(view.state.facet(language)).toBeNull();
});

// --- Adversarial review regressions (review #2-#5) and the save-conflict check (FEX-4) ---

describe("review: Editor edge cases", () => {
  async function setup(initial: string | null = "/wt/a.ts") {
    const w = await import("./wails");
    const { flushSync } = await import("svelte");
    const { EditorView } = await import("@codemirror/view");
    const { default: Host } = await import("./__stubs__/EditorHost.svelte");
    vi.mocked(w.readFile).mockReset();
    vi.mocked(w.readFile).mockImplementation(async (p: string) => "content of " + p);
    vi.mocked(w.writeFile).mockReset();
    vi.mocked(w.writeFile).mockImplementation(async () => {});
    vi.mocked(w.hunks).mockReset();
    vi.mocked(w.hunks).mockImplementation(async () => []);
    const r = render(Host, { props: { path: initial } });
    const cmView = () => {
      const el = document.querySelector(".cm-editor") as HTMLElement | null;
      return el ? EditorView.findFromDOM(el) : null;
    };
    const shown = () => cmView()?.state.doc.toString() ?? null;
    const type = (text: string) => { cmView()!.dispatch({ changes: { from: 0, insert: text } }); };
    const setPath = (p: string | null) => { r.component.setPath(p); flushSync(); };
    const bump = () => { r.component.bump(); flushSync(); };
    if (initial) await waitFor(() => expect(shown()).toBe("content of " + initial));
    return { w, r, shown, type, setPath, bump, cmView };
  }

  test("review #2: edits typed while the next file's read fails are saved, not dropped", async () => {
    const { w, type, setPath } = await setup();
    let rejectB: (e: Error) => void = () => {};
    vi.mocked(w.readFile).mockImplementation((p: string) =>
      p === "/wt/b.ts" ? new Promise<string>((_, rej) => { rejectB = rej; }) : Promise.resolve("content of " + p));
    setPath("/wt/b.ts");
    await waitFor(() => expect(w.readFile).toHaveBeenCalledWith("/wt/b.ts"));
    type("TYPED ");
    rejectB(new Error("file too large"));
    await waitFor(() => expect(w.writeFile).toHaveBeenCalledWith("/wt/a.ts", "TYPED content of /wt/a.ts"));
  });

  test("review #4: fs bumps of the target while a switch is blocked do not retry or re-notify", async () => {
    const { w, type, setPath, bump } = await setup();
    const { getItems } = await import("./stores/notifications.svelte");
    const before = getItems().filter((n) => n.title === "Save failed").length;
    vi.mocked(w.writeFile).mockRejectedValue(new Error("ENOENT"));
    type("EDIT ");
    setPath("/wt/b.ts");
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not be saved/));
    bump(); await new Promise((r) => setTimeout(r, 10));
    bump(); await new Promise((r) => setTimeout(r, 10));
    expect(getItems().filter((n) => n.title === "Save failed").length - before).toBe(1);
    expect(vi.mocked(w.writeFile).mock.calls.length).toBe(1);
  });

  test("review #5: overlapping saves land in order, so the newest text stays on disk", async () => {
    const { w, type } = await setup();
    type("ONE ");
    let release1: () => void = () => {};
    const disk: string[] = [];
    vi.mocked(w.writeFile).mockImplementationOnce((_p: string, c: string) =>
      new Promise<void>((res) => { release1 = () => { disk.push(c); res(); }; }));
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    type("TWO ");
    vi.mocked(w.writeFile).mockImplementationOnce(async (_p: string, c: string) => { disk.push(c); });
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    await new Promise((r) => setTimeout(r, 5));
    release1();
    await waitFor(() => expect(disk).toHaveLength(2));
    expect(disk.at(-1)).toBe("TWO ONE content of /wt/a.ts");
    expect(document.querySelector(".dirty-dot")).toBeNull();
  });

  test("review #3: a blocked switch offers Discard and Copy, and a preview swap keeps the edits", async () => {
    const { w, type, setPath, shown } = await setup();
    vi.mocked(w.writeFile).mockRejectedValue(new Error("ENOENT"));
    type("EDIT ");
    setPath("/wt/b.ts");
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not be saved/));
    // The natural escape, a previewable file, must not lose the edits.
    setPath("/wt/README.md");
    await new Promise((r) => setTimeout(r, 20));
    setPath("/wt/a.ts");
    await new Promise((r) => setTimeout(r, 20));
    expect(shown()).toBe("EDIT content of /wt/a.ts");
    // Copy puts the text on the clipboard; Discard drops the edits and moves on.
    setPath("/wt/b.ts");
    await waitFor(() => screen.getByRole("button", { name: /discard changes/i }));
    await fireEvent.click(screen.getByRole("button", { name: /copy text/i }));
    expect(w.clipboardSetText).toHaveBeenCalledWith("EDIT content of /wt/a.ts");
    await fireEvent.click(screen.getByRole("button", { name: /discard changes/i }));
    await waitFor(() => expect(shown()).toBe("content of /wt/b.ts"));
  });

  test("ErrBinaryFile: a binary file shows a notice, keeps no buffer, and Ctrl-S writes nothing", async () => {
    const { w, setPath, shown } = await setup();
    vi.mocked(w.readFile).mockRejectedValueOnce(new Error("fs: binary or non-UTF-8 file"));
    setPath("/wt/logo.bin");
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/not editable/));
    expect(shown()).toBeNull();
    await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
    await new Promise((r) => setTimeout(r, 10));
    expect(w.writeFile).not.toHaveBeenCalled();
  });

  describe("FEX-4: save conflicts", () => {
    test("a dirty buffer whose file changed on disk is not silently written over", async () => {
      const { w, type, bump } = await setup();
      type("MINE ");
      // The agent rewrites the file; the backend reports it.
      vi.mocked(w.readFile).mockImplementation(async () => "AGENT VERSION");
      bump();
      await new Promise((r) => setTimeout(r, 10));
      await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
      await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/changed on disk/));
      expect(w.writeFile).not.toHaveBeenCalled();
    });

    test("Overwrite writes the buffer; Reload takes the disk version", async () => {
      const { w, type, bump, shown } = await setup();
      type("MINE ");
      vi.mocked(w.readFile).mockImplementation(async () => "AGENT VERSION");
      bump();
      await new Promise((r) => setTimeout(r, 10));
      await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
      await fireEvent.click(await screen.findByRole("button", { name: /overwrite/i }));
      await waitFor(() => expect(w.writeFile).toHaveBeenCalledWith("/wt/a.ts", "MINE content of /wt/a.ts"));

      // A second round, answered with Reload.
      type("AGAIN ");
      vi.mocked(w.readFile).mockImplementation(async () => "AGENT VERSION 2");
      bump();
      await new Promise((r) => setTimeout(r, 10));
      await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
      await fireEvent.click(await screen.findByRole("button", { name: /reload from disk/i }));
      await waitFor(() => expect(shown()).toBe("AGENT VERSION 2"));
      expect(vi.mocked(w.writeFile).mock.calls).toHaveLength(1);
    });

    test("no prompt when the change on disk was our own save", async () => {
      const { w, type, bump } = await setup();
      type("ONE ");
      await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
      await waitFor(() => expect(w.writeFile).toHaveBeenCalledTimes(1));
      type("TWO ");
      vi.mocked(w.readFile).mockImplementation(async () => "ONE content of /wt/a.ts");
      bump(); // the watcher reports our own write
      await new Promise((r) => setTimeout(r, 10));
      await fireEvent.keyDown(document, { key: "s", ctrlKey: true });
      await waitFor(() => expect(w.writeFile).toHaveBeenCalledTimes(2));
      expect(screen.queryByText(/changed on disk/)).toBeNull();
    });
  });
});
