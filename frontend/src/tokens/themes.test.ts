// frontend/src/tokens/themes.test.ts
import { describe, it, expect } from "vitest";
import css from "./themes.css?raw";

const THEMES = ["gruvbox","tokyo-night","catppuccin","dracula","nord","rose-pine","one-dark","perch-cyan","light"] as const;
const PALETTE = ["--perch-bg","--perch-bg-elev","--perch-surface","--perch-border",
  "--perch-text","--perch-text-dim","--perch-accent","--perch-accent-fg",
  "--perch-ok","--perch-warn","--perch-err","--perch-info"] as const;

function extractBlock(theme: string): string {
  const marker = `[data-theme="${theme}"]`;
  const start  = css.indexOf(marker);
  if (start === -1) return "";
  const open  = css.indexOf("{", start);
  const close = css.indexOf("}", open);
  return css.slice(open, close + 1);
}

describe("themes.css", () => {
  it("has :root gruvbox defaults", () => { expect(css).toContain(":root"); });

  for (const theme of THEMES) {
    it(`[data-theme="${theme}"] defines all palette vars`, () => {
      const block = extractBlock(theme);
      expect(block).not.toBe("");
      for (const v of PALETTE) expect(block).toContain(v);
    });
  }

  it("gruvbox and dracula have different --perch-bg values", () => {
    const gBlock = extractBlock("gruvbox");
    const dBlock = extractBlock("dracula");
    const val = (b: string) => b.match(/--perch-bg:\s*([^;]+)/)?.[1]?.trim();
    expect(val(gBlock)).not.toBe(val(dBlock));
  });
});

// FEX-32: the per-theme contrast comments must describe the declared values,
// and the terminal's blue, cyan and magenta must be distinct.
describe("themes.css: audit regressions (FEX-32)", () => {
  const hex = (block: string, name: string) =>
    block.match(new RegExp(`--perch-${name}:\\s*(#[0-9a-fA-F]{6})`))?.[1]?.toLowerCase();
  const lum = (h: string) => {
    const c = [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16) / 255)
      .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
  };
  const ratio = (a: string, b: string) => {
    const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
  };
  function commentFor(theme: string): string {
    const at = css.indexOf(theme === "gruvbox" ? ":root" : `[data-theme="${theme}"]`);
    const end = css.lastIndexOf("*/", at);
    const start = css.lastIndexOf("/*", end);
    return css.slice(start, end);
  }

  for (const theme of THEMES) {
    it(`${theme}: the contrast comment matches the declared colors`, () => {
      const block = extractBlock(theme);
      const comment = commentFor(theme);
      for (const name of ["bg", "text", "text-dim", "border", "border-strong"]) {
        expect(comment).toContain(hex(block, name)!);
      }
      const dim = ratio(hex(block, "text-dim")!, hex(block, "bg")!);
      expect(dim).toBeGreaterThanOrEqual(4.5);
      expect(comment).toContain(`≈${dim.toFixed(2)}:1`);
    });

    it(`${theme}: ANSI blue, cyan and magenta are distinct`, () => {
      const block = extractBlock(theme);
      const blue = hex(block, "ansi-blue"), cyan = hex(block, "ansi-cyan"), magenta = hex(block, "ansi-magenta");
      expect(blue && cyan && magenta).toBeTruthy();
      expect(new Set([blue, cyan, magenta]).size).toBe(3);
    });
  }
});
