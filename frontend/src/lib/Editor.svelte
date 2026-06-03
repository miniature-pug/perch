<!-- frontend/src/lib/Editor.svelte -->
<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { EditorView, keymap, gutter, GutterMarker } from "@codemirror/view";
  import { EditorState, StateField, StateEffect } from "@codemirror/state";
  import { defaultKeymap, indentWithTab } from "@codemirror/commands";
  import { bracketMatching } from "@codemirror/language";
  import { javascript } from "@codemirror/lang-javascript";
  import { readFile, writeFile, hunks as fetchHunks, type Hunk } from "./wails";
  import { changedLinesFromHunks } from "./gutter";

  let { path, worktree }: { path: string | null; worktree: string } = $props();

  let container = $state<HTMLDivElement | null>(null);
  let view = $state<EditorView | null>(null);

  // Git gutter
  const setChangedLines = StateEffect.define<Set<number>>();
  const changedLinesField = StateField.define<Set<number>>({
    create: () => new Set(),
    update(val, tr) {
      for (const e of tr.effects) if (e.is(setChangedLines)) return e.value;
      return val;
    },
  });
  class ChangedMarker extends GutterMarker {
    toDOM() {
      const el = document.createElement("div");
      el.className = "cm-gutterElement perch-changed";
      el.textContent = "▎";
      return el;
    }
  }
  const changedMarker = new ChangedMarker();
  const changedGutter = gutter({
    class: "perch-git-gutter",
    lineMarker(v, line) {
      const no = v.state.doc.lineAt(line.from).number;
      return v.state.field(changedLinesField).has(no) ? changedMarker : null;
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
        javascript(),
        EditorView.lineWrapping,
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
