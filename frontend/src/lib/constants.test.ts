// frontend/src/lib/constants.test.ts
//
// Unit tests for the worktree color identity helpers in constants.ts.
// Covers stability, id-derivation, palette membership, and edge cases.

import { describe, it, expect, vi, afterEach } from "vitest";
import { WORKTREE_COLORS, worktreeColor, formatRelativeAge } from "./constants";

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

describe("worktreeColor(id, index) position-based assignment (F39)", () => {
  it("the first 8 workspaces (by position) each get a distinct color", () => {
    // Regardless of how the ids would hash, positions 0..7 map round-robin
    // onto the 8 distinct palette entries — no collisions in the common case.
    const colors = Array.from({ length: WORKTREE_COLORS.length }, (_, i) =>
      worktreeColor(`ws_${i}`, i),
    );
    expect(new Set(colors).size).toBe(WORKTREE_COLORS.length);
  });

  it("ignores the ids entirely when positions are supplied", () => {
    // Two ids that hash to the SAME bucket still get distinct colors when
    // their positions differ — proving position, not hash, drives the result.
    const a = worktreeColor("collision", 0);
    const b = worktreeColor("collision", 1);
    expect(a).toBe(WORKTREE_COLORS[0]);
    expect(b).toBe(WORKTREE_COLORS[1]);
    expect(a).not.toBe(b);
  });

  it("round-robins past the palette size (index wraps)", () => {
    for (let i = 0; i < WORKTREE_COLORS.length * 3; i++) {
      expect(worktreeColor("x", i)).toBe(WORKTREE_COLORS[i % WORKTREE_COLORS.length]);
    }
  });

  it("falls back to the stable hash when index is omitted or invalid", () => {
    // No index → hash path (unchanged legacy behavior).
    expect(worktreeColor("main")).toBe(worktreeColor("main"));
    // Negative / non-integer indices are rejected → hash path, still valid.
    expect(WORKTREE_COLORS as readonly string[]).toContain(worktreeColor("main", -1));
    expect(WORKTREE_COLORS as readonly string[]).toContain(worktreeColor("main", 1.5));
  });
});

describe("formatRelativeAge (F49b)", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns '' for empty string", () => {
    expect(formatRelativeAge("")).toBe("");
  });

  it("returns '' for an unparseable timestamp (isNaN guard)", () => {
    expect(formatRelativeAge("not-a-date")).toBe("");
  });

  it("renders 'today' for a same-day timestamp", () => {
    const now = new Date("2026-06-05T12:00:00Z");
    vi.useFakeTimers();
    vi.setSystemTime(now);
    expect(formatRelativeAge("2026-06-05T06:00:00Z")).toBe("today");
  });

  it("renders '1d ago' at exactly one day (numeric form, not 'yesterday')", () => {
    const now = new Date("2026-06-05T12:00:00Z");
    vi.useFakeTimers();
    vi.setSystemTime(now);
    expect(formatRelativeAge("2026-06-04T11:00:00Z")).toBe("1d ago");
  });

  it("renders 'Nd ago' for older timestamps", () => {
    const now = new Date("2026-06-05T12:00:00Z");
    vi.useFakeTimers();
    vi.setSystemTime(now);
    expect(formatRelativeAge("2026-05-31T12:00:00Z")).toBe("5d ago");
  });
});
