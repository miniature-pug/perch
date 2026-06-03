import type { Hunk } from "./wails";

/**
 * changedLinesFromHunks returns the set of 1-based line numbers in the NEW
 * file that lie within any hunk's added range. Drives the editor git gutter.
 */
export function changedLinesFromHunks(hunkList: Hunk[]): Set<number> {
  const changed = new Set<number>();
  for (const h of hunkList) {
    for (let i = h.newStart; i < h.newStart + h.newLines; i++) changed.add(i);
  }
  return changed;
}
