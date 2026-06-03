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

<section aria-label="preview" class="preview scrollable">
  {#if kind === "image"}
    <img src={path} alt={path} class="preview-img" />
  {:else}
    <div class="preview-body prose">{@html html}</div>
  {/if}
</section>

<style>
  /* ---------- Outer container ---------- */
  .preview {
    flex: 1;
    overflow-y: auto;
    padding: var(--perch-sp-3);
    background: var(--perch-bg);
    scrollbar-width: thin;
    scrollbar-color: var(--perch-border) transparent;
  }
  .preview::-webkit-scrollbar { width: 6px; }
  .preview::-webkit-scrollbar-track { background: transparent; }
  .preview::-webkit-scrollbar-thumb { background: var(--perch-border); border-radius: 3px; }
  .preview::-webkit-scrollbar-thumb:hover { background: var(--perch-text-dim); }

  /* ---------- Prose area ---------- */
  .prose {
    max-width: 720px;
    margin: 0 auto;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    line-height: 1.65;
    color: var(--perch-text);
  }

  /* Headings — larger than body per brief */
  :global(.prose h1) {
    font-size: 1.5em;
    font-weight: 600;
    margin: var(--perch-sp-3) 0 var(--perch-sp-2) 0;
    color: var(--perch-text);
    line-height: 1.2;
  }
  :global(.prose h2) {
    font-size: 1.25em;
    font-weight: 600;
    margin: var(--perch-sp-3) 0 var(--perch-sp-1) 0;
    color: var(--perch-text);
    border-bottom: 1px solid var(--perch-border);
    padding-bottom: 4px;
    line-height: 1.3;
  }
  :global(.prose h3),
  :global(.prose h4),
  :global(.prose h5),
  :global(.prose h6) {
    font-size: 1.05em;
    font-weight: 600;
    margin: var(--perch-sp-2) 0 var(--perch-sp-1) 0;
    color: var(--perch-text);
  }

  /* Paragraph + misc */
  :global(.prose p) {
    margin: 0 0 var(--perch-sp-2) 0;
  }
  :global(.prose ul),
  :global(.prose ol) {
    margin: 0 0 var(--perch-sp-2) var(--perch-sp-3);
    padding: 0;
  }
  :global(.prose li) {
    margin-bottom: 4px;
  }
  :global(.prose a) {
    color: var(--perch-accent);
    text-decoration: underline;
  }
  :global(.prose a:hover) {
    color: var(--perch-info);
  }
  :global(.prose blockquote) {
    margin: 0 0 var(--perch-sp-2) 0;
    padding: 4px var(--perch-sp-2);
    border-left: 3px solid var(--perch-border);
    color: var(--perch-text-dim);
  }
  :global(.prose hr) {
    border: none;
    border-top: 1px solid var(--perch-border);
    margin: var(--perch-sp-3) 0;
  }

  /* Inline code */
  :global(.prose code) {
    font-family: var(--perch-font-mono);
    font-size: 0.9em;
    background: var(--perch-surface);
    border-radius: 3px;
    padding: 1px 4px;
    color: var(--perch-text);
  }

  /* Code blocks */
  :global(.prose pre) {
    background: var(--perch-surface);
    border: 1px solid var(--perch-border);
    border-radius: 4px;
    padding: var(--perch-sp-2);
    overflow-x: auto;
    margin: 0 0 var(--perch-sp-2) 0;
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
    line-height: var(--perch-lh-code);
    scrollbar-width: thin;
    scrollbar-color: var(--perch-border) transparent;
  }
  :global(.prose pre code) {
    background: transparent;
    padding: 0;
    border-radius: 0;
    font-size: inherit;
  }

  /* Tables */
  :global(.prose table) {
    border-collapse: collapse;
    width: 100%;
    margin: 0 0 var(--perch-sp-2) 0;
    font-size: var(--perch-fs-body);
  }
  :global(.prose th),
  :global(.prose td) {
    border: 1px solid var(--perch-border);
    padding: 4px var(--perch-sp-1);
    text-align: left;
  }
  :global(.prose th) {
    background: var(--perch-surface);
    font-weight: 600;
  }

  /* Mermaid diagram — centered */
  :global(.prose svg),
  :global(.preview-body svg) {
    display: block;
    max-width: 100%;
    margin: var(--perch-sp-2) auto;
  }

  /* ---------- Image ---------- */
  .preview-img {
    display: block;
    max-width: 100%;
    height: auto;
    margin: 0 auto;
  }
</style>
