// frontend/src/lib/constants.test.ts
//
// Unit tests for the worktree color identity helpers in constants.ts.
// Covers stability, id-derivation, palette membership, and edge cases.

import { describe, it, expect } from "vitest";
import { WORKTREE_COLORS, worktreeColor } from "./constants";

describe("WORKTREE_COLORS palette", () => {
  it("has exactly 8 entries", () => {
    expect(WORKTREE_COLORS).toHaveLength(8);
  });

  it("all entries are unique", () => {
    const unique = new Set(WORKTREE_COLORS);
    expect(unique.size).toBe(WORKTREE_COLORS.length);
  });
});

describe("worktreeColor(id)", () => {
  it("is stable — same id returns identical color across many calls", () => {
    const id = "feat/my-feature";
    const first = worktreeColor(id);
    for (let i = 0; i < 100; i++) {
      expect(worktreeColor(id)).toBe(first);
    }
  });

  it("two ids that hash to different buckets return different colors", () => {
    // djb2 bucket check (computed offline): "main" -> 6, "dev" -> 2
    const colorA = worktreeColor("main");
    const colorB = worktreeColor("dev");
    expect(colorA).not.toBe(colorB);
  });

  it("every return value is a member of WORKTREE_COLORS", () => {
    const ids = [
      "main", "dev", "feat/login", "fix/bug-42", "release/v1",
      "hotfix", "feature-a", "feature-b", "worktree-1", "worktree-2",
      "a", "z", "123", "hello-world", "UPPER",
    ];
    for (const id of ids) {
      const color = worktreeColor(id);
      expect(WORKTREE_COLORS as readonly string[]).toContain(color);
    }
  });

  it("empty-string id returns a valid palette member without throwing", () => {
    let color: string;
    expect(() => { color = worktreeColor(""); }).not.toThrow();
    expect(WORKTREE_COLORS as readonly string[]).toContain(worktreeColor(""));
  });
});
