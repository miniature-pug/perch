import { describe, it, expect } from "vitest";
import { changedLinesFromHunks, gutterChangesFromHunks } from "./gutter";
import type { Hunk } from "./wails";

function hunk(p: Partial<Hunk>): Hunk {
  return { file: "f", index: 0, header: "", oldStart: 1, oldLines: 1, newStart: 1, newLines: 1, lines: [], ...p };
}

describe("changedLinesFromHunks", () => {
  it("returns empty set for no hunks", () => {
    expect(changedLinesFromHunks([]).size).toBe(0);
  });
  it("marks each line in a hunk's new range", () => {
    const s = changedLinesFromHunks([hunk({ newStart: 1, newLines: 2 })]);
    expect([...s].sort((a, b) => a - b)).toEqual([1, 2]);
  });
  it("unions lines across multiple hunks", () => {
    const s = changedLinesFromHunks([hunk({ newStart: 1, newLines: 1 }), hunk({ newStart: 10, newLines: 3 })]);
    expect([...s].sort((a, b) => a - b)).toEqual([1, 10, 11, 12]);
  });
  it("contributes no lines for a pure deletion (newLines 0)", () => {
    expect(changedLinesFromHunks([hunk({ newStart: 5, newLines: 0 })]).size).toBe(0);
  });
});

describe("gutterChangesFromHunks", () => {
  it("returns empty sets for no hunks", () => {
    const { changed, deleted } = gutterChangesFromHunks([]);
    expect(changed.size).toBe(0);
    expect(deleted.size).toBe(0);
  });

  it("marks add lines as changed, not deleted", () => {
    const h = hunk({
      newStart: 3, newLines: 2, oldStart: 3, oldLines: 0,
      lines: [{ kind: "add", text: "+foo" }, { kind: "add", text: "+bar" }],
    });
    const { changed, deleted } = gutterChangesFromHunks([h]);
    expect([...changed].sort((a, b) => a - b)).toEqual([3, 4]);
    expect(deleted.size).toBe(0);
  });

  it("marks del lines as deleted at the boundary without advancing pointer", () => {
    // A hunk that deletes 2 lines starting at new-file line 5 (pure deletion: newLines=0)
    const h = hunk({
      newStart: 5, newLines: 0, oldStart: 5, oldLines: 2,
      lines: [{ kind: "del", text: "-gone1" }, { kind: "del", text: "-gone2" }],
    });
    const { changed, deleted } = gutterChangesFromHunks([h]);
    // Pure deletion: newLines===0 path, boundary is newStart=5
    expect(deleted.has(5)).toBe(true);
    expect(changed.size).toBe(0);
  });

  it("marks ctx lines as changed (add range) and del lines as deleted in mixed hunk", () => {
    // Hunk: ctx line at 1, del line (boundary 2 before advancing), add line at 2
    // Simulates: -old line / +new line / ctx line
    const h = hunk({
      newStart: 1, newLines: 2, oldStart: 1, oldLines: 2,
      lines: [
        { kind: "del", text: "-removed" },  // boundary at ptr=1, not advance
        { kind: "add", text: "+added" },    // changed=1, ptr→2
        { kind: "ctx", text: " context" },  // ptr→3
      ],
    });
    const { changed, deleted } = gutterChangesFromHunks([h]);
    expect(changed.has(1)).toBe(true);
    expect(deleted.has(1)).toBe(true);
  });

  it("pure deletion hunk (newLines=0) contributes to deleted set at newStart", () => {
    const h = hunk({ newStart: 7, newLines: 0, oldStart: 7, oldLines: 3, lines: [] });
    const { changed, deleted } = gutterChangesFromHunks([h]);
    expect(deleted.has(7)).toBe(true);
    expect(changed.size).toBe(0);
  });

  it("addition-only hunk contributes only to changed set", () => {
    const h = hunk({
      newStart: 10, newLines: 3, oldStart: 10, oldLines: 0,
      lines: [
        { kind: "add", text: "+a" },
        { kind: "add", text: "+b" },
        { kind: "add", text: "+c" },
      ],
    });
    const { changed, deleted } = gutterChangesFromHunks([h]);
    expect([...changed].sort((a, b) => a - b)).toEqual([10, 11, 12]);
    expect(deleted.size).toBe(0);
  });

  it("backward-compat: changedLinesFromHunks uses range metadata, agrees with gutterChangesFromHunks on pure-add hunks", () => {
    // When all lines are "add", both functions should mark the same set
    const h = hunk({ newStart: 2, newLines: 2, lines: [{ kind: "add", text: "+x" }, { kind: "add", text: "+y" }] });
    const compat = changedLinesFromHunks([h]);
    const { changed } = gutterChangesFromHunks([h]);
    // Both should have lines 2 and 3
    expect([...compat].sort((a, b) => a - b)).toEqual([2, 3]);
    expect([...changed].sort((a, b) => a - b)).toEqual([2, 3]);
  });
});
