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
