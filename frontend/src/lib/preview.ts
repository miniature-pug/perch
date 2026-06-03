// frontend/src/lib/preview.ts
// Pure helpers: file-extension → preview kind mapping.

const MARKDOWN_EXTS  = new Set([".md", ".markdown"]);
const MERMAID_EXTS   = new Set([".mmd"]);
const IMAGE_EXTS     = new Set([".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp"]);

/** Returns true if the path should be rendered by <Preview> rather than <Editor>. */
export function isPreviewable(path: string | null): boolean {
  if (!path) return false;
  const ext = path.slice(path.lastIndexOf(".")).toLowerCase();
  return MARKDOWN_EXTS.has(ext) || MERMAID_EXTS.has(ext) || IMAGE_EXTS.has(ext);
}

/** Maps a previewable path to its Preview `kind` prop. Assumes isPreviewable(path) is true. */
export function previewKind(path: string): "markdown" | "mermaid" | "image" {
  const ext = path.slice(path.lastIndexOf(".")).toLowerCase();
  if (MARKDOWN_EXTS.has(ext)) return "markdown";
  if (MERMAID_EXTS.has(ext)) return "mermaid";
  return "image";
}
