<!-- frontend/src/lib/Editor.svelte -->
<script lang="ts">
  import { onMount, onDestroy, untrack } from "svelte";
  import { EditorView, keymap, gutter, GutterMarker } from "@codemirror/view";
  import { EditorState, StateField, StateEffect, EditorSelection } from "@codemirror/state";
  import { defaultKeymap, indentWithTab } from "@codemirror/commands";
  import { bracketMatching } from "@codemirror/language";
  import { perchSyntaxHighlighting } from "./highlight";
  import { search, searchKeymap, highlightSelectionMatches } from "@codemirror/search";
  import { javascript } from "@codemirror/lang-javascript";
  import { css }        from "@codemirror/lang-css";
  import { html }       from "@codemirror/lang-html";
  import { json }       from "@codemirror/lang-json";
  import { markdown }   from "@codemirror/lang-markdown";
  import { python }     from "@codemirror/lang-python";
  import { go }         from "@codemirror/lang-go";
  import { readFile, writeFile, hunks as fetchHunks, clipboardSetText, clipboardText, type Hunk } from "./wails";
  import { gutterChangesFromHunks } from "./gutter";
  import { MIME_TEXT } from "./constants";
  import { addBlocking } from "./stores/notifications.svelte";

  let {
    path,
    worktree,
    reloadToken = 0,
    visible = true,
    onSendToAgent,
  }: {
    path: string | null;
    worktree: string;
    // The parent bumps this value when files change on disk. A change
    // reloads the file, but only when there are no unsaved edits. See the
    // load effect below.
    reloadToken?: number;
    // This is false when the editor is mounted but off-screen, for example
    // on the agent pane or diff pane. It gates the Ctrl-S shortcut, so a
    // hidden editor never captures it.
    visible?: boolean;
    onSendToAgent?: (text: string) => void;
  } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let view = $state<EditorView | null>(null);

  // Selection tracking for the send-to-agent action
  let selectionText = $state<string>("");

  // Dirty (unsaved) state. True when the document has changed since the
  // last load or save.
  let dirty = $state<boolean>(false);

  // ---------------------------------------------------------------------------
  // Language detection by filename extension
  // Installed packages: lang-javascript, lang-css, lang-html, lang-json,
  //   lang-markdown, lang-python, lang-go.
  // Not installed: @codemirror/language-data. This package would add full
  //   language coverage, including Rust, Java, C/C++, Ruby, Shell, YAML,
  //   and TOML.
  // ---------------------------------------------------------------------------
  function languageForPath(p: string) {
    const ext = p.split(".").pop()?.toLowerCase() ?? "";
    switch (ext) {
      case "js":
      case "mjs":
      case "cjs":
        return javascript();
      case "jsx":
        return javascript({ jsx: true });
      case "ts":
      case "mts":
      case "cts":
        return javascript({ typescript: true });
      case "tsx":
        return javascript({ typescript: true, jsx: true });
      case "css":
        return css();
      case "html":
      case "htm":
      case "svelte":
        return html();
      case "json":
      case "jsonc":
        return json();
      case "md":
      case "markdown":
        return markdown();
      case "py":
      case "pyi":
        return python();
      case "go":
        return go();
      default:
        return javascript(); // fallback
    }
  }

  // ---------------------------------------------------------------------------
  // Git gutter: change tracking for added and deleted lines
  // ---------------------------------------------------------------------------
  interface GutterState { changed: Set<number>; deleted: Set<number>; }

  const setChangedLines = StateEffect.define<GutterState>();
  const changedLinesField = StateField.define<GutterState>({
    create: () => ({ changed: new Set(), deleted: new Set() }),
    update(val, tr) {
      for (const e of tr.effects) if (e.is(setChangedLines)) return e.value;
      return val;
    },
  });

  class AddedMarker extends GutterMarker {
    toDOM() {
      const el = document.createElement("div");
      el.className = "cm-gutterElement perch-gutter-add";
      el.textContent = "▎";
      return el;
    }
  }
  class DeletedMarker extends GutterMarker {
    toDOM() {
      const el = document.createElement("div");
      el.className = "cm-gutterElement perch-gutter-del";
      el.textContent = "▎";
      return el;
    }
  }
  const addedMarker   = new AddedMarker();
  const deletedMarker = new DeletedMarker();

  const changedGutter = gutter({
    class: "perch-git-gutter",
    lineMarker(v, line) {
      const no = v.state.doc.lineAt(line.from).number;
      const gs = v.state.field(changedLinesField);
      if (gs.deleted.has(no)) return deletedMarker;
      if (gs.changed.has(no)) return addedMarker;
      return null;
    },
  });

  // ---------------------------------------------------------------------------
  // Selection listener for the send-to-agent action
  // ---------------------------------------------------------------------------
  const selectionListener = EditorView.updateListener.of((update) => {
    if (update.selectionSet || update.docChanged) {
      const { from, to } = update.state.selection.main;
      selectionText = from === to ? "" : update.state.sliceDoc(from, to);
    }
    // mark dirty on any user-driven document change
    if (update.docChanged) {
      dirty = true;
    }
  });

  function handleSendToAgent() {
    if (onSendToAgent && selectionText) {
      onSendToAgent(selectionText);
    }
  }

  // Generation counter. Each call to `load()` claims a generation number. A
  // newer load can start before this call's async reads resolve, for
  // example on a file switch or a `reloadToken` bump. When that happens,
  // this call's generation number no longer matches, and the code drops
  // the stale result. So a slow `readFile(A)` call that resolves after
  // `readFile(B)` can never show file A's content over file B's.
  let loadGen = 0;
  // The path whose content is now in the view. This lets `load()`
  // tell a same-file reload apart from a real file switch. A same-file
  // reload keeps the caret position and scroll position. A real file
  // switch resets to a fresh state, with the caret at the top.
  let renderedPath: string | null = null;

  // Clipboard keymap (B4). CodeMirror leaves copy, cut, and paste to the
  // browser's native clipboard. This clipboard is unreliable under
  // WebKit2GTK. So the code binds Ctrl-Shift-C and Ctrl-Shift-V to the host
  // clipboard binding, the same route used elsewhere in the cockpit.
  // Ctrl-Shift-C copies the main selection. It returns false on an empty
  // selection, so the shortcut is never captured when there is nothing to
  // copy. Ctrl-Shift-V pastes the host clipboard text over the current
  // selection, using an ordinary CodeMirror transaction.
  const clipboardKeymap = keymap.of([
    {
      key: "Ctrl-Shift-c",
      run: (v) => {
        const { from, to } = v.state.selection.main;
        if (from === to) return false;
        clipboardSetText(v.state.sliceDoc(from, to)).catch(() => {});
        return true;
      },
    },
    {
      key: "Ctrl-Shift-v",
      run: (v) => {
        clipboardText()
          .then((text) => { if (text) v.dispatch(v.state.replaceSelection(text)); })
          .catch(() => {});
        return true;
      },
    },
  ]);

  // Build a fresh EditorState for a file. The code calls this on first
  // mount and on a file switch. Resetting the selection and scroll to the
  // top is the intended behavior then.
  function buildState(p: string, content: string): EditorState {
    return EditorState.create({
      doc: content,
      extensions: [
        changedLinesField,
        changedGutter,
        search({ top: true }),
        highlightSelectionMatches(),
        clipboardKeymap,
        keymap.of([...searchKeymap, ...defaultKeymap, indentWithTab]),
        bracketMatching(),
        // syntax highlighting, using a HighlightStyle mapped to perch CSS variables
        perchSyntaxHighlighting,
        selectionListener,
        languageForPath(p),
        EditorView.lineWrapping,
        EditorView.theme({
          "&": {
            background: "var(--perch-bg)",
            color:      "var(--perch-text)",
            height:     "100%",
            fontFamily: "var(--perch-font-mono)",
            fontSize:   "var(--perch-fs-code)",
            lineHeight: "var(--perch-lh-code)",
          },
          ".cm-content": { caretColor: "var(--perch-accent)" },
          ".cm-cursor": { borderLeftColor: "var(--perch-accent)" },
          ".cm-selectionBackground, ::selection":
            { background: "color-mix(in srgb, var(--perch-accent) 20%, transparent)" },
          ".cm-gutters": {
            background:  "var(--perch-bg-elev)",
            borderRight: "1px solid var(--perch-border)",
            color:       "var(--perch-text-dim)",
          },
          ".cm-activeLineGutter": { background: "transparent" },
          ".cm-activeLine": {
            background: "color-mix(in srgb, var(--perch-accent) 6%, transparent)",
          },
          ".perch-gutter-add": { color: "var(--perch-ok)",  paddingLeft: "2px" },
          ".perch-gutter-del": { color: "var(--perch-err)", paddingLeft: "2px" },
          ".cm-scroller": {
            fontFamily: "var(--perch-font-mono)",
            fontSize:   "var(--perch-fs-code)",
            lineHeight: "var(--perch-lh-code)",
          },
          // Search panel styling using perch tokens
          ".cm-search": {
            background:  "var(--perch-bg-elev)",
            borderTop:   "1px solid var(--perch-border)",
            padding:     "4px 8px",
            fontFamily:  "var(--perch-font-mono)",
            fontSize:    "var(--perch-fs-code)",
            color:       "var(--perch-text)",
          },
          ".cm-search input": {
            background:  "var(--perch-bg)",
            color:       "var(--perch-text)",
            border:      "1px solid var(--perch-border-strong)",
            borderRadius: "3px",
            padding:     "1px 4px",
            fontFamily:  "var(--perch-font-mono)",
            fontSize:    "var(--perch-fs-code)",
          },
          ".cm-search button": {
            background:  "var(--perch-bg)",
            color:       "var(--perch-text)",
            border:      "1px solid var(--perch-border-strong)",
            borderRadius: "3px",
            padding:     "1px 6px",
            cursor:      "pointer",
          },
          ".cm-searchMatch": {
            background: "color-mix(in srgb, var(--perch-warn) 30%, transparent)",
            outline:    "1px solid var(--perch-warn)",
          },
          ".cm-searchMatch-selected": {
            background: "color-mix(in srgb, var(--perch-accent) 40%, transparent)",
          },
        }),
      ],
    });
  }

  async function load(p: string, cancelled: () => boolean = () => false) {
    const gen = ++loadGen;
    const [content, hunkList] = await Promise.all([
      readFile(p),
      fetchHunks(worktree, p).catch(() => [] as Hunk[]),
    ]);
    // Drop a superseded result. A newer load may have claimed a later
    // generation, or the component may be tearing down (cancelled). This
    // guards against a stale file flashing in, and against dispatching
    // into a view that the code is about to destroy.
    if (cancelled() || gen !== loadGen) return;
    const gutterState = gutterChangesFromHunks(hunkList);

    const sameFile = view !== null && renderedPath === p;
    if (!view) {
      if (!container) return;
      view = new EditorView({ state: buildState(p, content), parent: container });
    } else if (!sameFile) {
      // File switch. The code builds a fresh state, and resets the caret
      // and scroll to the top on purpose.
      view.setState(buildState(p, content));
    } else {
      // Same-file reload. An on-disk change bumped `reloadToken` while the
      // buffer was clean. Keep the caret, selection, and scroll position.
      // Replace the document only when the text actually changed, and
      // clamp the old selection to the new length. A bare `setState` call
      // here would reset the cursor to 0 and jump the scroll to the top on
      // every reload.
      const current = view.state.doc.toString();
      if (current !== content) {
        const max = content.length;
        const sel = view.state.selection;
        const ranges = sel.ranges.map((r) =>
          EditorSelection.range(Math.min(r.anchor, max), Math.min(r.head, max)),
        );
        view.dispatch({
          changes: { from: 0, to: view.state.doc.length, insert: content },
          selection: EditorSelection.create(ranges, sel.mainIndex),
          scrollIntoView: false,
        });
      }
    }
    renderedPath = p;
    // Refresh the git gutter to match the newly loaded hunks. This also
    // clears stale markers on a same-file reload, when the changes were
    // just staged away.
    view.dispatch({ effects: setChangedLines.of(gutterState) });
    // The `setState` call and the doc-replacing dispatch both fire
    // `docChanged` through the update listener. Clear `dirty`, so the
    // newly loaded buffer starts clean.
    dirty = false;
  }

  async function save() {
    if (!path || !view) return;
    try {
      await writeFile(path, view.state.doc.toString());
      dirty = false;
    } catch (e) {
      // A failed write must not look successful. Keep the buffer dirty,
      // and raise a blocking notification, so the code never silently
      // loses the unsaved edit.
      const msg = e instanceof Error ? e.message : String(e);
      addBlocking(path, "Save failed", `Could not write ${path}: ${msg}`);
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === "s") {
      // The editor can stay mounted while hidden, for example on the agent
      // pane or diff pane. Ignore the shortcut then, so it does not
      // capture Ctrl-S from the terminal, or save an off-screen file.
      if (!visible) return;
      e.preventDefault();
      save();
    }
  }

  // Drag the selected text as application/x-perch-text. This matches the
  // MIME type in DragDrop.svelte.
  function handleDragStart(e: DragEvent) {
    if (!selectionText || !e.dataTransfer) return;
    e.dataTransfer.effectAllowed = "copy";
    e.dataTransfer.setData(MIME_TEXT, selectionText);
    e.dataTransfer.setData("text/plain", selectionText);
  }

  // Tracks the file the editor now holds. This lets the code tell a
  // file switch apart from an in-place reload signal.
  let loadedPath: string | null = null;

  // Load on a file switch. On an external change to the same file, when
  // `reloadToken` bumps, reload only when there are no unsaved edits. This
  // stops a background change from discarding the user's draft. The code
  // reads `dirty` and `loadedPath` untracked, so this effect depends only
  // on `path` and `reloadToken`.
  $effect(() => {
    const p = path;
    reloadToken;
    // Cancellation flag for this run. The cleanup function below flips
    // this flag when the effect re-runs, for example when `path` or
    // `reloadToken` changes, or when the component is destroyed. So an
    // in-flight load that resolves afterward drops its stale result. See
    // `load()`.
    let cancelled = false;
    untrack(() => {
      if (!p) { loadedPath = null; return; }
      if (p !== loadedPath || !dirty) {
        loadedPath = p;
        load(p, () => cancelled);
      }
    });
    return () => { cancelled = true; };
  });

  onMount(() => document.addEventListener("keydown", handleKeyDown));
  onDestroy(() => {
    document.removeEventListener("keydown", handleKeyDown);
    // A dirty buffer here means unsaved edits. Destroying the view
    // discards the document. So the code saves the document first, with
    // the same care as `save()`, instead of silently dropping the user's
    // work on a session switch. `save()` captures the document
    // synchronously before the write, and raises a blocking notification
    // if the write fails. So a failed save always surfaces, and the code
    // never hides it.
    if (dirty) save();
    view?.destroy();
    view = null;
  });
</script>

{#if path}
  <section aria-label="editor" class="editor-wrap">
    <div bind:this={container} class="cm-host"></div>

    {#if dirty}
      <span class="dirty-dot" aria-label="Unsaved changes" title="Unsaved changes">●</span>
    {/if}

    {#if onSendToAgent && selectionText}
      <!-- Draggable with application/x-perch-text. The button also acts as a drag handle. -->
      <button
        class="send-to-agent-btn"
        aria-label="Send to agent"
        draggable={true}
        onclick={handleSendToAgent}
        ondragstart={handleDragStart}
      >Send to agent ↗</button>
    {/if}
  </section>
{/if}

<style>
  .editor-wrap {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    min-width: 0;
    overflow: hidden;
    position: relative;
  }

  .cm-host {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    /* CodeMirror mounts .cm-editor here. It needs height:100% to fill the space. */
  }

  /* Target the CodeMirror editor element itself. The rule is unscoped
     (:global), so it can pierce into the shadow content CodeMirror
     creates. */
  :global(.cm-editor) {
    height: 100%;
  }

  :global(.perch-git-gutter) {
    width: 6px;
    min-width: 6px;
  }

  /* unsaved indicator */
  .dirty-dot {
    position: absolute;
    top: var(--perch-sp-1, 4px);
    right: var(--perch-sp-2, 8px);
    font-size: 10px;
    line-height: 1;
    color: var(--perch-warn);
    pointer-events: none;
    z-index: var(--perch-z-editor-dirty);
    user-select: none;
  }

  .send-to-agent-btn {
    position: absolute;
    bottom: var(--perch-sp-2);
    right: var(--perch-sp-2);
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 10px;
    background: var(--perch-bg-elev);
    color: var(--perch-accent);
    border: 1px solid var(--perch-accent);
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    cursor: pointer;
    transition:
      background var(--perch-dur) var(--perch-ease),
      color var(--perch-dur) var(--perch-ease);
    z-index: var(--perch-z-editor-send);
  }
  .send-to-agent-btn:hover {
    background: color-mix(in srgb, var(--perch-accent) 15%, var(--perch-bg-elev));
  }
  .send-to-agent-btn:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
  }
</style>
