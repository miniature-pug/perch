<!-- frontend/src/lib/Editor.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { EditorView, keymap, gutter, GutterMarker } from "@codemirror/view";
  import { EditorState, StateField, StateEffect } from "@codemirror/state";
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
  import { readFile, writeFile, hunks as fetchHunks, type Hunk } from "./wails";
  import { gutterChangesFromHunks } from "./gutter";
  import { MIME_TEXT } from "./constants";

  let {
    path,
    worktree,
    onSendToAgent,
  }: {
    path: string | null;
    worktree: string;
    onSendToAgent?: (text: string) => void;
  } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let view = $state<EditorView | null>(null);

  // Selection tracking for send-to-agent affordance
  let selectionText = $state<string>("");

  // N-10: dirty/unsaved state — true when document has been modified since last load/save
  let dirty = $state<boolean>(false);

  // ---------------------------------------------------------------------------
  // Language detection by filename extension
  // Installed packages: lang-javascript, lang-css, lang-html, lang-json,
  //   lang-markdown, lang-python, lang-go.
  // NOT installed: @codemirror/language-data (would give full coverage incl.
  //   Rust, Java, C/C++, Ruby, Shell, YAML, TOML, etc.)
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
  // Git gutter — change tracking (added and deleted lines)
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
  // Selection listener for send-to-agent affordance
  // ---------------------------------------------------------------------------
  const selectionListener = EditorView.updateListener.of((update) => {
    if (update.selectionSet || update.docChanged) {
      const { from, to } = update.state.selection.main;
      selectionText = from === to ? "" : update.state.sliceDoc(from, to);
    }
    // N-10: mark dirty on any user-driven document change
    if (update.docChanged) {
      dirty = true;
    }
  });

  function handleSendToAgent() {
    if (onSendToAgent && selectionText) {
      onSendToAgent(selectionText);
    }
  }

  async function load(p: string) {
    const [content, hunkList] = await Promise.all([
      readFile(p),
      fetchHunks(worktree, p).catch(() => [] as Hunk[]),
    ]);
    const gutterState = gutterChangesFromHunks(hunkList);

    const state = EditorState.create({
      doc: content,
      extensions: [
        changedLinesField,
        changedGutter,
        search({ top: true }),
        highlightSelectionMatches(),
        keymap.of([...searchKeymap, ...defaultKeymap, indentWithTab]),
        bracketMatching(),
        // H-9: syntax highlighting via perch CSS-variable-mapped HighlightStyle
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
    if (view) {
      view.setState(state);
    } else if (container) {
      view = new EditorView({ state, parent: container });
    }
    // N-10: setState/new EditorView fires docChanged via the update listener;
    // overwrite immediately so the freshly-loaded file starts clean.
    dirty = false;
    if (gutterState.changed.size > 0 || gutterState.deleted.size > 0) {
      view?.dispatch({ effects: setChangedLines.of(gutterState) });
    }
  }

  async function save() {
    if (!path || !view) return;
    await writeFile(path, view.state.doc.toString());
    // N-10: clear dirty flag after successful save
    dirty = false;
  }

  function handleKeyDown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save(); }
  }

  // N-24: drag selected text as application/x-perch-text (matches DragDrop.svelte MIME)
  function handleDragStart(e: DragEvent) {
    if (!selectionText || !e.dataTransfer) return;
    e.dataTransfer.effectAllowed = "copy";
    e.dataTransfer.setData(MIME_TEXT, selectionText);
    e.dataTransfer.setData("text/plain", selectionText);
  }

  $effect(() => { if (path) load(path); });

  onMount(() => document.addEventListener("keydown", handleKeyDown));
  onDestroy(() => {
    document.removeEventListener("keydown", handleKeyDown);
    view?.destroy();
    view = null;
  });
</script>

{#if path}
  <section aria-label="editor" class="editor-wrap">
    <div bind:this={container} class="cm-host"></div>

    {#if dirty}
      <!-- N-10: unsaved indicator dot -->
      <span class="dirty-dot" aria-label="Unsaved changes" title="Unsaved changes">●</span>
    {/if}

    {#if onSendToAgent && selectionText}
      <!-- N-24: draggable with application/x-perch-text; button also acts as drag affordance -->
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
    /* CodeMirror mounts .cm-editor here; it needs height:100% to fill */
  }

  /* Target the CodeMirror editor element itself (unscoped to pierce shadow) */
  :global(.cm-editor) {
    height: 100%;
  }

  :global(.perch-git-gutter) {
    width: 6px;
    min-width: 6px;
  }

  /* N-10: unsaved indicator */
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
