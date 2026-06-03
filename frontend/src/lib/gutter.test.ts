import { describe, it, expect } from "vitest";
import { changedLinesFromHunks } from "./gutter";
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
