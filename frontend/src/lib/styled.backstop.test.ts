// frontend/src/lib/styled.backstop.test.ts
//
// Static backstop: every *.svelte in src/lib/ and src/App.svelte must contain a
// <style block, or be explicitly exempted. A failing test here means a component
// shipped without scoped CSS — the design-token system would be disconnected from it.

import { readdirSync, readFileSync } from "fs";
import { join, basename } from "path";
import { test, expect } from "vitest";

// Components legitimately exempt from the <style> requirement.
// ThemeProvider is a logic-only wrapper: it sets data-theme/data-density on
// <html> and renders its children — it has no visual surface of its own.
const EXEMPT = new Set(["ThemeProvider.svelte"]);

const LIB_DIR = join(__dirname, ".");
const APP_FILE = join(__dirname, "..", "App.svelte");

function collectSvelteFiles(): string[] {
  const libFiles = readdirSync(LIB_DIR)
    .filter((f) => f.endsWith(".svelte"))
    .map((f) => join(LIB_DIR, f));
  return [...libFiles, APP_FILE];
}

test("every Svelte component has a <style> block", () => {
  const files = collectSvelteFiles();
  expect(files.length).toBeGreaterThan(0); // sanity: directory wasn't empty

  const unstyled: string[] = [];

  for (const filePath of files) {
    const name = basename(filePath);
    if (EXEMPT.has(name)) continue;
    const source = readFileSync(filePath, "utf-8");
    if (!source.includes("<style")) {
      unstyled.push(name);
    }
  }

  expect(
    unstyled,
    `Components missing <style> block (add styles or add to EXEMPT): ${unstyled.join(", ")}`
  ).toEqual([]);
});

test("EXEMPT list only contains files that actually lack <style>", () => {
  // Guard against the allowlist growing stale: if someone adds styles to an
  // exempted component, this test will remind them to remove the exemption.
  const files = collectSvelteFiles();
  const filesByName = new Map(files.map((f) => [basename(f), f]));

  for (const name of EXEMPT) {
    const filePath = filesByName.get(name);
    if (!filePath) continue; // file deleted — also OK, exemption will be harmless
    const source = readFileSync(filePath, "utf-8");
    expect(
      source.includes("<style"),
      `${name} is in EXEMPT but now has a <style> block — remove it from the allowlist`
    ).toBe(false);
  }
});
