<!-- frontend/src/lib/Preview.svelte -->
<script lang="ts">
  import { marked } from "marked";
  import mermaid from "mermaid";
  import DOMPurify from "dompurify";

  let {
    path, kind, content,
  }: { path: string; kind: "markdown" | "mermaid" | "image"; content: string } = $props();

  let html = $state("");

  $effect(() => {
    let cancelled = false;
    if (kind === "markdown" && content) {
      Promise.resolve(marked(content)).then((h) => {
        if (!cancelled) html = DOMPurify.sanitize(h as string, { USE_PROFILES: { html: true, svg: true, svgFilters: true } });
      });
    } else if (kind === "mermaid" && content) {
      mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
      mermaid.render("preview-mermaid", content).then(({ svg }) => {
        if (!cancelled) html = DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true, html: true } });
      });
    }
    return () => { cancelled = true; };
  });
</script>

<section aria-label="preview" class="preview">
  {#if kind === "image"}
    <img src={path} alt={path} class="preview-img" />
  {:else}
    <div class="preview-body">{@html html}</div>
  {/if}
</section>
