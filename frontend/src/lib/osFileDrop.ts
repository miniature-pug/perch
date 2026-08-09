// frontend/src/lib/osFileDrop.ts
//
// OS file-manager drop routing. When a file is dragged from the desktop file
// manager onto perch, WebKitGTK's DOM drop event exposes NO real path (the
// File.path field is a non-standard Electron-ism that is undefined here). The
// only reliable source of an ABSOLUTE path is Wails' native file drop, enabled
// via options.DragAndDrop{EnableFileDrop:true} (see app/options.go), which emits
// the "wails:file-drop" event and surfaces it as window.runtime.OnFileDrop.
//
// OnFileDrop can only be registered once process-wide, so a single global
// handler here routes each drop to the correct pane by hit-testing the drop
// coordinates against the [data-drop-pane] attribute a fileDrop-enabled DragDrop
// puts on its drop-zone. The in-app file-tree/editor drag is a separate,
// path-carrying DOM drop handled inside DragDrop.svelte and is untouched by this.

import { writeToPty } from "./wails";

// Attribute a fileDrop-enabled DragDrop sets on its .drop-zone so an OS file
// drop can be routed to the pane under the drop point.
export const DROP_PANE_ATTR = "data-drop-pane";

// Shell-quote a path so an @mention survives paths containing spaces (or other
// shell metacharacters). Wrap in single quotes and escape any embedded single
// quote via the '\'' idiom, e.g. it's -> 'it'\''s'.
export function shellQuote(p: string): string {
  return `'${p.replace(/'/g, "'\\''")}'`;
}

// Encode a single dropped path as a shell-quoted @mention with a trailing space,
// as UTF-8 bytes ready for writeToPty. Matches the @'<path>' convention the
// file tree and editor already use.
export function mentionBytes(path: string): number[] {
  return Array.from(new TextEncoder().encode(`@${shellQuote(path)} `));
}

// Resolve which pane sits under the drop point (x, y in viewport CSS pixels),
// or null when the drop did not land on a fileDrop-enabled pane.
export function paneIdAt(x: number, y: number): string | null {
  const el = document.elementFromPoint(x, y);
  const zone = el?.closest(`[${DROP_PANE_ATTR}]`) as HTMLElement | null;
  return zone?.getAttribute(DROP_PANE_ATTR) ?? null;
}

// Route a native OS file drop (absolute paths) to the pane under (x, y). Each
// path is sent as its own @mention, mirroring the previous per-file behavior.
export async function routeOsFileDrop(x: number, y: number, paths: string[]): Promise<void> {
  const paneId = paneIdAt(x, y);
  if (!paneId || paths.length === 0) return;
  for (const p of paths) {
    await writeToPty(paneId, mentionBytes(p));
  }
}

// Register the single global Wails OnFileDrop handler. Returns an off-fn.
// No-op (and safe) when the Wails runtime is absent — unit tests and any
// non-desktop context — so callers can register unconditionally.
export function registerOsFileDrop(): () => void {
  const rt = typeof window !== "undefined" ? window.runtime : undefined;
  if (!rt || typeof rt.OnFileDrop !== "function") return () => {};
  // useDropTarget=false: we hit-test the coordinates ourselves against
  // [data-drop-pane], so we do not depend on the --wails-drop-target CSS marker.
  rt.OnFileDrop((x, y, paths) => { void routeOsFileDrop(x, y, paths); }, false);
  return () => { rt.OnFileDropOff?.(); };
}
