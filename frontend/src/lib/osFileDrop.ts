// frontend/src/lib/osFileDrop.ts
//
// OS file-manager drop routing. When the user drags a file from the desktop
// file manager onto perch, WebKitGTK's DOM drop event exposes no real path.
// The File.path field is a non-standard Electron-ism, and it is undefined
// here. The only reliable source of an absolute path is Wails' native file
// drop. It is enabled through options.DragAndDrop{EnableFileDrop:true} (see
// app/options.go), which emits the "wails:file-drop" event and surfaces it
// as window.runtime.OnFileDrop.
//
// The app can register OnFileDrop only once, process-wide, so a single
// global handler here routes each drop to the correct pane. It hit-tests
// the drop coordinates against the [data-drop-pane] attribute that a
// fileDrop-enabled DragDrop puts on its drop zone. The in-app file-tree and
// editor drag is a separate, path-carrying DOM drop. DragDrop.svelte
// handles that drag, and this file does not touch it.

import { writeToPty } from "./wails";
import { addBlocking } from "./stores/notifications.svelte";

// Attribute that a fileDrop-enabled DragDrop sets on its .drop-zone, so the
// code can route an OS file drop to the pane under the drop point.
export const DROP_PANE_ATTR = "data-drop-pane";

// Shell-quote a path so an @mention survives paths that contain spaces or
// other shell metacharacters. Wrap the path in single quotes, and escape
// any embedded single quote with the '\'' idiom. For example, it's becomes
// 'it'\''s'.
export function shellQuote(p: string): string {
  return `'${p.replace(/'/g, "'\\''")}'`;
}

// The one @mention format for every entry point: an OS file drop, a
// file-tree drag, and "Send to agent" (FEX-27, FEC-35). A plain path stays
// bare (@/repo/src/main.go), the form the agent CLIs resolve; a path with a
// space or a shell metacharacter is single-quoted so it stays one token.
// The trailing space ends the mention.
const PLAIN_PATH = /^[A-Za-z0-9._\/@+=:,%~-]+$/;
export function mentionText(path: string): string {
  return `@${PLAIN_PATH.test(path) ? path : shellQuote(path)} `;
}

// Encode a single dropped path as an @mention (see mentionText), as UTF-8
// bytes ready for writeToPty.
export function mentionBytes(path: string): number[] {
  return Array.from(new TextEncoder().encode(mentionText(path)));
}

// Resolve which pane sits under the drop point (x, y in viewport CSS
// pixels). Returns null when the drop did not land on a fileDrop-enabled
// pane.
export function paneIdAt(x: number, y: number): string | null {
  const el = document.elementFromPoint(x, y);
  const zone = el?.closest(`[${DROP_PANE_ATTR}]`) as HTMLElement | null;
  return zone?.getAttribute(DROP_PANE_ATTR) ?? null;
}

// Route a native OS file drop (absolute paths) to the pane under (x, y).
// The code sends each path as its own @mention, matching the earlier
// per-file behavior.
export async function routeOsFileDrop(x: number, y: number, paths: string[]): Promise<void> {
  const paneId = paneIdAt(x, y);
  if (!paneId || paths.length === 0) return;
  for (const p of paths) {
    await writeToPty(paneId, mentionBytes(p));
  }
}

// Register the single global Wails OnFileDrop handler. Returns an
// unregister function. This is a no-op, and safe, when the Wails runtime
// is absent, for example in unit tests or any non-desktop context, so
// callers can register it unconditionally.
export function registerOsFileDrop(): () => void {
  const rt = typeof window !== "undefined" ? window.runtime : undefined;
  if (!rt || typeof rt.OnFileDrop !== "function") return () => {};
  // useDropTarget=false: the code hit-tests the coordinates itself against
  // [data-drop-pane], so it does not depend on the --wails-drop-target CSS
  // marker.
  rt.OnFileDrop((x, y, paths) => {
    routeOsFileDrop(x, y, paths).catch((e) => {
      addBlocking("", "Could not send the dropped file", String(e), "error");
    });
  }, false);
  return () => { rt.OnFileDropOff?.(); };
}
