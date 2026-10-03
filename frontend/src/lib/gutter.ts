import type { Hunk } from "./wails";

/**
 * GutterChanges separates added or changed lines from deleted lines.
 *
 * changed: 1-based line numbers in the new file that contain additions or
 *          changes. These are hunk lines with kind "add".
 *
 * deleted: 1-based line numbers in the new file at the deletion boundary.
 *          A pure deletion hunk (newLines === 0) has no new-file lines, so
 *          the boundary is Math.max(1, newStart). A mixed hunk also adds
 *          to deleted for each hunk line with kind "del".
 */
export interface GutterChanges {
  changed: Set<number>;
  deleted: Set<number>;
}

/**
 * gutterChangesFromHunks walks each hunk's lines. It tracks the new-file
 * line pointer, and sorts each physical hunk line into one of three cases:
 *   - "add": mark that new-file line as changed, and advance the pointer.
 *   - "ctx": advance the pointer only.
 *   - "del": mark the current new-file boundary as deleted. Do not advance
 *     the pointer.
 *
 * For a pure-deletion hunk (newLines === 0, no "add" or "ctx" lines), the
 * boundary defaults to Math.max(1, newStart).
 */
export function gutterChangesFromHunks(hunkList: Hunk[]): GutterChanges {
  const changed = new Set<number>();
  const deleted = new Set<number>();

  for (const h of hunkList) {
    // A staged hunk's line numbers are relative to the index, not to the
    // working file the editor shows, so only unstaged hunks are plotted
    // (wiring-gitfs #4).
    if (h.staged) continue;
    // Pure deletion: newLines === 0. Mark the boundary line as deleted.
    if (h.newLines === 0) {
      deleted.add(Math.max(1, h.newStart));
      continue;
    }

    let ptr = h.newStart;
    for (const line of h.lines) {
      if (line.kind === "add") {
        changed.add(ptr);
        ptr++;
      } else if (line.kind === "ctx") {
        ptr++;
      } else {
        // "del": mark the current boundary. Do not advance the new-file pointer.
        deleted.add(Math.max(1, ptr));
      }
    }
  }

  return { changed, deleted };
}
