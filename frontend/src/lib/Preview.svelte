<!-- frontend/src/lib/Preview.svelte -->
<script lang="ts">
  import { marked } from "marked";
  import mermaid from "mermaid";

  let {
    path, kind, content,
  }: { path: string; kind: "markdown" | "mermaid" | "image"; content: string } = $props();

  let html = $state("");

  $effect(() => {
    if (kind === "markdown" && content) {
      Promise.resolve(marked(content)).then((h) => { html = h as string; });
    } else if (kind === "mermaid" && content) {
      mermaid.initialize({ startOnLoad: false });
      mermaid.render("preview-mermaid", content).then(({ svg }) => { html = svg; });
    }
  });
</script>

<section aria-label="preview" class="preview">
  {#if kind === "image"}
    <img src={path} alt={path} class="preview-img" />
  {:else}
    <div class="preview-body">{@html html}</div>
  {/if}
</section>
