<!-- frontend/src/lib/Editor.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { EditorView, keymap, gutter, GutterMarker } from "@codemirror/view";
  import { EditorState, StateField, StateEffect } from "@codemirror/state";
  import { defaultKeymap, indentWithTab } from "@codemirror/commands";
  import { bracketMatching } from "@codemirror/language";
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
            border:      "1px solid var(--perch-border)",
            borderRadius: "3px",
            padding:     "1px 4px",
            fontFamily:  "var(--perch-font-mono)",
            fontSize:    "var(--perch-fs-code)",
          },
          ".cm-search button": {
            background:  "var(--perch-bg)",
            color:       "var(--perch-text)",
            border:      "1px solid var(--perch-border)",
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
    if (gutterState.changed.size > 0 || gutterState.deleted.size > 0) {
      view?.dispatch({ effects: setChangedLines.of(gutterState) });
    }
  }

  async function save() {
    if (!path || !view) return;
    await writeFile(path, view.state.doc.toString());
  }

  function handleKeyDown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save(); }
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
    {#if onSendToAgent && selectionText}
      <button
        class="send-to-agent-btn"
        aria-label="Send to agent"
        onclick={handleSendToAgent}
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
    border-radius: 4px;
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    cursor: pointer;
    transition:
      background var(--perch-dur) var(--perch-ease),
      color var(--perch-dur) var(--perch-ease);
    z-index: 10;
  }
  .send-to-agent-btn:hover {
    background: color-mix(in srgb, var(--perch-accent) 15%, var(--perch-bg-elev));
  }
  .send-to-agent-btn:focus-visible {
    outline: 2px solid var(--perch-accent);
    outline-offset: 2px;
  }
</style>
