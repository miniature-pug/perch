// frontend/src/lib/preview.ts
// Pure helpers that map a file extension to a preview kind.

const MARKDOWN_EXTS  = new Set([".md", ".markdown"]);
const MERMAID_EXTS   = new Set([".mmd"]);
const IMAGE_EXTS     = new Set([".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp"]);

/** Returns true when <Preview> should render the path, instead of <Editor>. */
export function isPreviewable(path: string | null): boolean {
  if (!path) return false;
  const i = path.lastIndexOf(".");
  const ext = i === -1 ? "" : path.slice(i).toLowerCase();
  if (!ext) return false;
  return MARKDOWN_EXTS.has(ext) || MERMAID_EXTS.has(ext) || IMAGE_EXTS.has(ext);
}

/** Maps a previewable path to its Preview `kind` prop. Assumes isPreviewable(path) returns true. */
export function previewKind(path: string): "markdown" | "mermaid" | "image" {
  const i = path.lastIndexOf(".");
  const ext = i === -1 ? "" : path.slice(i).toLowerCase();
  if (MARKDOWN_EXTS.has(ext)) return "markdown";
  if (MERMAID_EXTS.has(ext)) return "mermaid";
  return "image";
}

// External links open in the system browser. Only plain web and mail
// links qualify.
export function isExternalUrl(href: string): boolean {
  return /^(https?:|mailto:)/i.test(href);
}

// Resolve a relative link against the previewed file's directory.
// Returns null for anchors, absolute URLs with a scheme, and empty links.
//
// Each segment is percent-decoded BEFORE ".." is resolved, so "%2e%2e" or
// "..%2F.." cannot slip past the normalization and escape the worktree
// after decoding (review #7). A segment that decodes to something holding a
// separator or NUL, or does not decode, rejects the link.
export function resolveRelative(fromFile: string, href: string): string | null {
  const target = href.split("#")[0].split("?")[0];
  if (!target || /^[a-z][a-z0-9+.-]*:/i.test(target)) return null;
  const base = target.startsWith("/") ? [] : fromFile.split("/").slice(0, -1);
  for (const raw of target.split("/")) {
    let part: string;
    try { part = decodeURIComponent(raw); } catch { return null; }
    if (/[\\/\u0000]/.test(part)) return null;
    if (part === "" || part === ".") continue;
    if (part === "..") base.pop();
    else base.push(part);
  }
  return "/" + base.filter(Boolean).join("/");
}
