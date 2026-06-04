import type { Hunk } from "./wails";

/**
 * changedLinesFromHunks returns the set of 1-based line numbers in the NEW
 * file that lie within any hunk's added range. Drives the editor git gutter.
 *
 * Uses the hunk's range metadata (newStart / newLines) directly — same as the
 * original implementation — so it works even when the lines array is absent/empty.
 *
 * Kept for backward compatibility. Use gutterChangesFromHunks for add/delete
 * distinction based on per-line kind metadata.
 */
export function changedLinesFromHunks(hunkList: Hunk[]): Set<number> {
  const changed = new Set<number>();
  for (const h of hunkList) {
    for (let i = h.newStart; i < h.newStart + h.newLines; i++) changed.add(i);
  }
  return changed;
}

/**
 * GutterChanges distinguishes added/changed lines from deleted lines.
 *
 * changed: 1-based line numbers in the new file that contain additions or
 *          modifications (hunk lines that have kind "add").
 *
 * deleted: 1-based line numbers in the new file at the deletion boundary.
 *          A pure deletion hunk (newLines === 0) has no new-file lines, so
 *          the boundary is Math.max(1, newStart). Mixed hunks also contribute
 *          to deleted when a "del" line is encountered.
 */
export interface GutterChanges {
  changed: Set<number>;
  deleted: Set<number>;
}

/**
 * gutterChangesFromHunks walks each hunk's lines, tracking the new-file line
 * pointer, and categorises each physical hunk line:
 *   - "add" → marks that new-file line as changed, advances pointer
 *   - "ctx" → advances pointer only
 *   - "del" → marks current new-file boundary as deleted, does NOT advance
 *
 * For a pure-deletion hunk (newLines === 0, no "add"/"ctx" lines), the
 * boundary defaults to Math.max(1, newStart).
 */
export function gutterChangesFromHunks(hunkList: Hunk[]): GutterChanges {
  const changed = new Set<number>();
  const deleted = new Set<number>();

  for (const h of hunkList) {
    // Pure deletion: newLines === 0, mark the boundary line as deleted.
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
        // "del": mark the current boundary, don't advance new-file pointer
        deleted.add(Math.max(1, ptr));
      }
    }
  }

  return { changed, deleted };
}
