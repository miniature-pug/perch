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
    workspaceId = "",
    onSendToAgent,
    onShowPreview,
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
    // The owning session, so a "Save failed" notification links back to it.
    workspaceId?: string;
    onSendToAgent?: (text: string) => void;
    // Set for a previewable file (Markdown, Mermaid) opened as source: shows
    // a button that switches back to the rendered preview (FEX-23).
    onShowPreview?: () => void;
  } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let view = $state<EditorView | null>(null);

  // Selection tracking for the send-to-agent action
  let selectionText = $state<string>("");

  // Dirty (unsaved) state. True when the document has changed since the
  // last load or save.
  let dirty = $state<boolean>(false);
  // A plain mirror of `dirty` for the logic. Svelte hands teardown code the
  // value a $state had before the batch that destroyed the component, so an
  // edit typed just before a switch would read as clean in onDestroy and be
  // dropped. The script reads `isDirty`; the template reads `dirty`.
  let isDirty = false;
  function setDirty(v: boolean) { isDirty = v; dirty = v; }

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
        // Plain text. JavaScript highlighting and indentation are wrong for
        // most other files (YAML, Rust, shell, Makefiles) (FEX-30).
        return [];
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
      setDirty(true);
    }
  });

  function handleSendToAgent() {
    if (onSendToAgent && selectionText) {
      onSendToAgent(selectionText);
    }
  }

  // Generation counter. Each call to `sync()` claims a generation number. A
  // newer load can start before this call's async reads resolve, for
  // example on a file switch or a `reloadToken` bump. When that happens,
  // this call's generation number no longer matches, and the code drops
  // the stale result. So a slow `readFile(A)` call that resolves after
  // `readFile(B)` can never show file A's content over file B's.
  let loadGen = 0;
  // The path whose content is now in the view. This lets `sync()`
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

  // Path relative to the worktree, for the git hunk lookup. The backend's
  // Hunks binding takes a worktree-relative path and rejects an absolute one,
  // so passing the absolute path left the gutter permanently empty (FEX-12).
  // A file outside the worktree has no git hunks to show.
  function relToWorktree(p: string): string | null {
    if (!worktree) return null;
    const prefix = worktree.endsWith("/") ? worktree : worktree + "/";
    return p.startsWith(prefix) ? p.slice(prefix.length) : null;
  }

  // An inline error shown in place of the document. `sync` sets it when a
  // file cannot be read (too large, not a regular file, deleted, outside the
  // root). `switchBlocked` names a file whose unsaved edits could not be
  // written when the user switched away from it, so it is still on screen.
  let loadError = $state<{ path: string; message: string } | null>(null);
  let switchBlocked = $state<string | null>(null);
  let destroyed = false;

  function errMessage(e: unknown): string {
    return e instanceof Error ? e.message : String(e);
  }

  function baseName(p: string): string {
    return p.split("/").pop() || p;
  }

  // Keep the CodeMirror view inside the current host element. `{#if path}`
  // re-creates the host when the path goes null and back, which would leave a
  // surviving view detached and the editor blank (FEX-31).
  function attachView() {
    if (view && container && view.dom.parentElement !== container) {
      container.appendChild(view.dom);
      view.requestMeasure();
    }
  }

  // Bring the view in line with `p`. Every call claims a generation; a newer
  // call supersedes an older one at each await, so a slow read can never show
  // a stale file, and a reload bump during a switch cannot strand the old
  // buffer under the new path (FEX-17). The rules:
  //   - A dirty buffer for ANOTHER file is saved to ITS OWN path first, the
  //     same autosave the component already does on teardown (FEX-1, FEX-2).
  //     If that save fails, the switch stops and the old file stays on screen,
  //     still dirty, so nothing is lost and nothing is written to the new file.
  //   - A dirty buffer for THIS file is never replaced by a reload.
  //   - A failed read shows an error and clears the view, so the old text can
  //     never sit under the new path and be saved into it (FEX-3).
  async function sync(p: string | null) {
    const gen = ++loadGen;
    const current = () => gen === loadGen && !destroyed;

    if (!p) {
      // The host is gone. Save a dirty buffer, then drop the view. If the save
      // fails, keep the (detached) view and its edits: a later path re-attaches
      // it, and teardown tries the save again.
      if (view && isDirty && !(await save())) return;
      if (!current()) return;
      view?.destroy();
      view = null;
      renderedPath = null;
      loadError = null;
      switchBlocked = null;
      return;
    }

    attachView();

    if (view && isDirty && renderedPath && renderedPath !== p) {
      const leaving = renderedPath;
      const ok = await save();
      if (!current()) return;
      if (!ok) { switchBlocked = leaving; return; }
    }
    switchBlocked = null;

    // Same file, unsaved edits: an external change must not discard them.
    if (view && renderedPath === p && isDirty) return;

    let content: string;
    let hunkList: Hunk[];
    try {
      const rel = relToWorktree(p);
      [content, hunkList] = await Promise.all([
        readFile(p),
        rel ? fetchHunks(worktree, rel).catch(() => [] as Hunk[]) : Promise.resolve([] as Hunk[]),
      ]);
    } catch (e) {
      if (!current()) return;
      // Nothing of the previous file may stay under this path. The previous
      // buffer is clean here (it was saved above, or it was never dirty), so
      // dropping the view loses nothing.
      view?.destroy();
      view = null;
      renderedPath = null;
      setDirty(false);
      loadError = { path: p, message: errMessage(e) };
      return;
    }
    if (!current()) return;

    // The user typed while the read was in flight.
    if (isDirty && view) {
      // Same file: keep the new edits, skip this reload.
      if (renderedPath === p) return;
      // Another file: run the switch again, which saves those edits first.
      void sync(p);
      return;
    }

    const gutterState = gutterChangesFromHunks(hunkList);
    attachView();
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
      if (view.state.doc.toString() !== content) {
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
    loadError = null;
    // Refresh the git gutter to match the newly loaded hunks. This also
    // clears stale markers on a same-file reload, when the changes were
    // just staged away.
    view.dispatch({ effects: setChangedLines.of(gutterState) });
    // The doc-replacing dispatch fires `docChanged` through the update
    // listener. Clear `dirty`, so the newly loaded buffer starts clean.
    setDirty(false);
  }

  // The save in flight, so a second request for the same document (Ctrl-S
  // during a switch, a reload racing a switch) shares one write.
  let inflight: { path: string; doc: unknown; promise: Promise<boolean> } | null = null;

  // Save the buffer to the file it was loaded from (`renderedPath`), never to
  // the live `path` prop: during a switch or teardown that prop already names
  // the NEXT file (FEX-1). Resolves true when the write succeeded.
  function save(): Promise<boolean> {
    if (!view || !renderedPath) return Promise.resolve(false);
    const target = renderedPath;
    const snap = view.state.doc;
    if (inflight && inflight.path === target && inflight.doc === snap) return inflight.promise;
    let promise!: Promise<boolean>;
    promise = (async () => {
      try {
        await writeFile(target, snap.toString());
        // Keystrokes typed while the write was in flight are not on disk yet.
        // CodeMirror's Text is immutable, so identity means "unchanged" (FEX-16).
        if (view && renderedPath === target && view.state.doc === snap) setDirty(false);
        if (switchBlocked === target) switchBlocked = null;
        return true;
      } catch (e) {
        // A failed write must not look successful. Keep the buffer dirty,
        // and raise a blocking notification, so the code never silently
        // loses the unsaved edit.
        addBlocking(workspaceId, "Save failed", `Could not write ${target}: ${errMessage(e)}`);
        return false;
      } finally {
        if (inflight?.promise === promise) inflight = null;
      }
    })();
    inflight = { path: target, doc: snap, promise };
    return promise;
  }

  function handleKeyDown(e: KeyboardEvent) {
    // Match on the letter, case-insensitively, so Caps Lock does not turn
    // the shortcut off (FEX-29).
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      // The editor can stay mounted while hidden, for example on the agent
      // pane or diff pane. Ignore the shortcut then, so it does not
      // capture Ctrl-S from the terminal, or save an off-screen file.
      if (!visible) return;
      e.preventDefault();
      void save().then((ok) => {
        // A switch that stopped on a failed save resumes once the retry works.
        if (ok && path && renderedPath !== path) void sync(path);
      });
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

  // Load on a file switch, and on an external change to the same file when
  // `reloadToken` bumps. `sync` decides what to do and never replaces
  // unsaved edits. The body runs untracked, so this effect depends only on
  // `path` and `reloadToken`.
  $effect(() => {
    const p = path;
    reloadToken;
    untrack(() => { void sync(p); });
  });

  onMount(() => document.addEventListener("keydown", handleKeyDown));
  onDestroy(() => {
    destroyed = true;
    document.removeEventListener("keydown", handleKeyDown);
    // A dirty buffer here means unsaved edits. Destroying the view
    // discards the document. So the code saves the document first, with
    // the same care as `save()`, instead of silently dropping the user's
    // work on a session switch. `save()` captures the document and its own
    // path synchronously before the write, and raises a blocking
    // notification if the write fails.
    if (isDirty) void save();
    view?.destroy();
    view = null;
  });
</script>

{#if path}
  <section aria-label="editor" class="editor-wrap">
    {#if switchBlocked}
      <p class="editor-banner" role="alert">
        Unsaved changes to {baseName(switchBlocked)} could not be saved, so it is still open. Press Ctrl-S to retry.
      </p>
    {/if}
    <div bind:this={container} class="cm-host"></div>
    {#if loadError && loadError.path === path}
      <p class="editor-error" role="alert">Could not open {baseName(loadError.path)}: {loadError.message}</p>
    {/if}

    {#if onShowPreview}
      <button class="show-preview-btn" onclick={onShowPreview}>Show preview</button>
    {/if}

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

  .editor-error,
  .editor-banner {
    margin: 0;
    padding: var(--perch-sp-2) var(--perch-sp-3);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
  }
  .editor-error { color: var(--perch-err); }
  .editor-banner {
    color: var(--perch-text);
    background: color-mix(in srgb, var(--perch-warn) 15%, var(--perch-bg-elev));
    border-bottom: 1px solid var(--perch-border);
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

  .show-preview-btn {
    position: absolute;
    top: var(--perch-sp-1);
    right: calc(var(--perch-sp-2) + 16px);
    padding: 2px 8px;
    background: var(--perch-bg-elev);
    color: var(--perch-text);
    border: 1px solid var(--perch-border-strong);
    border-radius: var(--perch-radius-sm);
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-caption);
    cursor: pointer;
    z-index: var(--perch-z-editor-send);
  }
  .show-preview-btn:focus-visible {
    outline: var(--perch-ring-w) solid var(--perch-accent);
    outline-offset: 2px;
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
