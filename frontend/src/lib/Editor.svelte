<!-- frontend/src/lib/Editor.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { EditorView, keymap, gutter, GutterMarker } from "@codemirror/view";
  import { EditorState, StateField, StateEffect } from "@codemirror/state";
  import { defaultKeymap, indentWithTab } from "@codemirror/commands";
  import { bracketMatching } from "@codemirror/language";
  import { javascript } from "@codemirror/lang-javascript";
  import { css }        from "@codemirror/lang-css";
  import { html }       from "@codemirror/lang-html";
  import { json }       from "@codemirror/lang-json";
  import { markdown }   from "@codemirror/lang-markdown";
  import { python }     from "@codemirror/lang-python";
  import { go }         from "@codemirror/lang-go";
  import { readFile, writeFile, hunks as fetchHunks, type Hunk } from "./wails";
  import { changedLinesFromHunks } from "./gutter";

  let { path, worktree }: { path: string | null; worktree: string } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let view = $state<EditorView | null>(null);

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

  // Git gutter
  const setChangedLines = StateEffect.define<Set<number>>();
  const changedLinesField = StateField.define<Set<number>>({
    create: () => new Set(),
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

  // changedLinesFromHunks returns Set<number> of added/modified lines only.
  // All lines in the set are additions; we show them with the ok (green) marker.
  // The deletedMarker is defined for future use when the gutter helper can
  // distinguish add vs delete lines.
  const changedGutter = gutter({
    class: "perch-git-gutter",
    lineMarker(v, line) {
      const no = v.state.doc.lineAt(line.from).number;
      return v.state.field(changedLinesField).has(no) ? addedMarker : null;
    },
  });

  async function load(p: string) {
    const [content, hunkList] = await Promise.all([
      readFile(p),
      fetchHunks(worktree, p).catch(() => [] as Hunk[]),
    ]);
    const changed = changedLinesFromHunks(hunkList);

    const state = EditorState.create({
      doc: content,
      extensions: [
        changedLinesField,
        changedGutter,
        keymap.of([...defaultKeymap, indentWithTab]),
        bracketMatching(),
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
        }),
      ],
    });
    if (view) {
      view.setState(state);
    } else if (container) {
      view = new EditorView({ state, parent: container });
    }
    if (changed.size > 0) view?.dispatch({ effects: setChangedLines.of(changed) });
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
</style>
