// frontend/src/tokens/tokens.test.ts
import { describe, it, expect } from "vitest";
import css from "./tokens.css?raw";

describe("tokens.css", () => {
  it("defines --perch-fs-code as 14px on :root", () => {
    expect(css).toContain("--perch-fs-code: 14px");
  });
  it("defines --perch-sp-1 through --perch-sp-8", () => {
    for (let i = 1; i <= 8; i++) expect(css).toContain(`--perch-sp-${i}:`);
  });
  it("defines dense density multiplier 0.75", () => {
    expect(css).toContain('[data-density="dense"]');
    expect(css).toContain("--perch-density-scale: 0.75");
  });
  it("defines comfortable multiplier 1", () => {
    expect(css).toContain('[data-density="comfortable"]');
    expect(css).toContain("--perch-density-scale: 1");
  });
  it("defines ultra multiplier 1.25", () => {
    expect(css).toContain('[data-density="ultra"]');
    expect(css).toContain("--perch-density-scale: 1.25");
  });
  it("defines motion tokens", () => {
    expect(css).toContain("--perch-dur: 120ms");
    expect(css).toContain("--perch-ease: cubic-bezier(.4,0,.2,1)");
  });
  it("defines font stack tokens", () => {
    expect(css).toContain("--perch-font-sans:");
    expect(css).toContain("--perch-font-mono:");
  });
});
