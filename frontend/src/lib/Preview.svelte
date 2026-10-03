<!-- frontend/src/lib/Preview.svelte -->
<script module lang="ts">
  import mermaid from "mermaid";
  import DOMPurify from "dompurify";

  // Markdown task lists render as <input type="checkbox">. Every other input
  // is removed, and a kept checkbox is always disabled, so the preview shows
  // done/open items without offering a form control (review #6).
  let purifyHooked = false;
  function hookPurify() {
    if (purifyHooked) return;
    purifyHooked = true;
    DOMPurify.addHook("afterSanitizeAttributes", (node) => {
      if (node.nodeName !== "INPUT") return;
      const el = node as Element;
      if ((el.getAttribute("type") ?? "").toLowerCase() !== "checkbox") { el.remove(); return; }
      el.setAttribute("disabled", "");
    });
  }
  hookPurify();

  // Configure mermaid once per app, not on every render (FEX-25).
  let mermaidReady = false;
  function ensureMermaid() {
    if (mermaidReady) return;
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
    mermaidReady = true;
  }
  // A unique render id per call, so overlapping renders never share mermaid's
  // temporary DOM node (FEX-25).
  let renderSeq = 0;

  // Sanitizer settings for rendered markdown. The preview is rendered into the
  // app's own document (not a sandboxed frame), so a README or an
  // agent-written file must not be able to restyle the cockpit (a style element
  // that relabels or hides the approval card), cover it with a fixed overlay
  // (a style attribute), or post a form (FEX-10). Links are handled by
  // onPreviewClick below, never by navigation.
  export const MARKDOWN_PURIFY = {
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    FORBID_TAGS: ["style", "link", "meta", "base", "form", "button", "textarea",
                  "select", "option", "iframe", "frame", "object", "embed", "dialog"],
    FORBID_ATTR: ["id", "name", "style", "class", "action", "formaction", "target"],
  };

</script>

<script lang="ts">
  import { marked } from "marked";
  import { isExternalUrl, resolveRelative } from "./preview";

  let {
    path, kind, content, visible = true, src = "", onOpenFile, onEditSource,
  }: {
    path: string;
    // The URL an image preview loads (the backend's /wt-file/ handler,
    // FEX-11). An absolute filesystem path never loads in the webview.
    src?: string;
    kind: "markdown" | "mermaid" | "image";
    content: string;
    // False when the preview is mounted but off-screen, for example on the agent
    // pane or the diff view. This gates the render side-effect, so marked and
    // mermaid never run while hidden. The effect re-runs and renders the
    // current content when the preview becomes visible again.
    visible?: boolean;
    // Open a file a relative link points at. Without it, relative links do
    // nothing.
    onOpenFile?: (absPath: string) => void;
    // Switch this file to the source editor (FEX-23).
    onEditSource?: () => void;
  } = $props();

  let html = $state("");

  $effect(() => {
    // Read `visible` first, so Svelte tracks it. While hidden, return before
    // reading kind or content, so a background content change does not
    // re-render an off-screen preview.
    if (!visible) return;
    let cancelled = false;
    if (kind === "markdown" && content) {
      Promise.resolve(marked(content)).then((h) => {
        if (!cancelled) html = DOMPurify.sanitize(h as string, MARKDOWN_PURIFY) as string;
      }).catch((e) => {
        if (!cancelled) html = errorBanner(e);
      });
    } else if (kind === "mermaid" && content) {
      ensureMermaid();
      mermaid.render(`preview-mermaid-${++renderSeq}`, content).then(({ svg }) => {
        // Mermaid draws arrowheads as <marker> elements, referenced through
        // marker-end="url(#id)". Forbidding `id` here would strip the marker
        // ids, and the arrowheads would vanish. So this sanitize call does NOT
        // forbid `id` on the diagram SVG (verified against mermaid's
        // .attr("id", …) and url(#…) markers). `name` stays forbidden, and the
        // render still runs strict-mode and svg-profile sanitizing. This change
        // restores function only; it does not relax security.
        if (!cancelled) html = DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true, html: true }, FORBID_ATTR: ['name'] });
      }).catch((e) => {
        // A malformed diagram must not blank the pane with an unhandled
        // rejection. Show an inline error banner instead.
        if (!cancelled) html = errorBanner(e);
      });
    } else {
      // Empty content: clear any stale rendered diagram or markdown.
      html = "";
    }
    return () => { cancelled = true; };
  });

  function errorBanner(e: unknown): string {
    const msg = e instanceof Error ? e.message : String(e);
    return DOMPurify.sanitize(
      `<div class="preview-error" role="alert">Could not render preview: ${msg}</div>`,
      { USE_PROFILES: { html: true } },
    );
  }

  // A link in rendered content must never navigate the app's own webview
  // away from the cockpit (FEX-10). Web links open in the system browser;
  // a relative link opens the file it names; anything else does nothing.
  function onPreviewClick(e: MouseEvent) {
    const a = (e.target as Element | null)?.closest?.("a");
    if (!a) return;
    e.preventDefault();
    const href = a.getAttribute("href") ?? a.getAttribute("xlink:href") ?? "";
    if (isExternalUrl(href)) {
      window.runtime?.BrowserOpenURL?.(href);
      return;
    }
    const target = resolveRelative(path, href);
    if (target) onOpenFile?.(target);
  }
</script>

<section aria-label="preview" class="preview scrollable">
  {#if onEditSource && kind !== "image"}
    <div class="preview-toolbar">
      <button class="btn btn-sm" onclick={onEditSource}>Edit source</button>
    </div>
  {/if}
  {#if kind === "image"}
    {#if src}
      <img {src} alt={path} class="preview-img" />
    {:else}
      <p class="preview-empty">This image is outside the session's worktree and cannot be previewed.</p>
    {/if}
  {:else}
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="preview-body prose" onclick={onPreviewClick}>{@html html}</div>
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
  .preview::-webkit-scrollbar { width: var(--perch-scrollbar-w); }
  .preview::-webkit-scrollbar-track { background: transparent; }
  .preview::-webkit-scrollbar-thumb { background: var(--perch-border); border-radius: var(--perch-scrollbar-radius); }
  .preview::-webkit-scrollbar-thumb:hover { background: var(--perch-text-dim); }

  .preview-empty { color: var(--perch-text-dim); margin: 0; }

  .preview-toolbar {
    display: flex;
    justify-content: flex-end;
    margin-bottom: var(--perch-sp-2);
  }

  /* ---------- Prose area ---------- */
  .prose {
    max-width: 720px;
    margin: 0 auto;
    font-family: var(--perch-font-sans);
    font-size: var(--perch-fs-body);
    line-height: 1.65;
    color: var(--perch-text);
  }

  /* Headings */
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
    border-radius: var(--perch-radius-sm);
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

  /* Mermaid diagram: centered */
  :global(.prose svg),
  :global(.preview-body svg) {
    display: block;
    max-width: 100%;
    margin: var(--perch-sp-2) auto;
  }

  /* ---------- Inline render-error banner ---------- */
  :global(.preview-body .preview-error) {
    padding: var(--perch-sp-2);
    border: 1px solid var(--perch-err);
    border-radius: var(--perch-radius-sm);
    background: color-mix(in srgb, var(--perch-err) 10%, var(--perch-bg));
    color: var(--perch-err);
    font-family: var(--perch-font-mono);
    font-size: var(--perch-fs-code);
  }

  /* ---------- Image ---------- */
  .preview-img {
    display: block;
    max-width: 100%;
    height: auto;
    margin: 0 auto;
  }
</style>
