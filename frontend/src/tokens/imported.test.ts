// Backstop: assert that main.ts imports both token stylesheets.
// This test catches a future regression if someone removes the imports from main.ts.
// It needs no full browser run.
import { readFileSync } from "fs";
import { resolve } from "path";
import { describe, it, expect } from "vitest";

describe("token stylesheet imports in main.ts", () => {
  const mainSrc = readFileSync(resolve(__dirname, "../main.ts"), "utf-8");

  it('imports ./tokens/tokens.css', () => {
    expect(mainSrc).toMatch(/import\s+["']\.\/tokens\/tokens\.css["']/);
  });

  it('imports ./tokens/themes.css', () => {
    expect(mainSrc).toMatch(/import\s+["']\.\/tokens\/themes\.css["']/);
  });
});
