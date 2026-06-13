/**
 * themes.spec.ts
 *
 * For each of the 9 themes: boot the app with that theme in settings,
 * assert --perch-bg resolves to the expected value (from themes.css),
 * assert .app-root backgroundColor is non-transparent,
 * take a full-page screenshot.
 * Then assert all 9 --perch-bg values are distinct.
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent } from "./_mock";
import { PREVIEW_PORT } from "../preview-port.mjs";

// Relative to cwd (frontend/) where playwright runs.
const SCREENSHOT_DIR = "./e2e/__screenshots__";

// Ground truth extracted directly from frontend/src/tokens/themes.css
const THEME_BG_MAP: Record<string, string> = {
  "gruvbox":     "#282828",
  "tokyo-night": "#1a1b26",
  "catppuccin":  "#1e1e2e",
  "dracula":     "#282a36",
  "nord":        "#2e3440",
  "rose-pine":   "#191724",
  "one-dark":    "#282c34",
  "perch-cyan":  "#0d1117",
  "light":       "#f5f0e8",
};

const THEMES = Object.keys(THEME_BG_MAP);

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

for (const theme of THEMES) {
  test(`theme: ${theme} — --perch-bg resolves and .app-root is painted`, async ({ page }) => {
    await page.addInitScript({ content: buildInitScriptContent({ settings: { theme } }) });
    await page.goto("/");
    await page.waitForSelector("#app", { timeout: 10000 });
    // Wait for onMount async work: settings.load() + layout.restore()
    await page.waitForTimeout(1500);

    // Assertion 1: --perch-bg must match the known value for this theme (NON-VACUOUS)
    const percbBg = await page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--perch-bg").trim()
    );

    const expected = THEME_BG_MAP[theme];
    expect(
      percbBg,
      `--perch-bg for theme "${theme}": expected "${expected}", got "${percbBg}"`
    ).toBe(expected);

    // Assertion 2: .app-root background-color must NOT be transparent
    const appRootBg = await page.evaluate(() => {
      const el = document.querySelector(".app-root");
      if (!el) return "ELEMENT_MISSING";
      return getComputedStyle(el).backgroundColor;
    });

    expect(
      appRootBg,
      `[${theme}] .app-root background-color should not be transparent, got: ${appRootBg}`
    ).not.toBe("rgba(0, 0, 0, 0)");
    expect(
      appRootBg,
      `[${theme}] .app-root background-color should not be 'transparent', got: ${appRootBg}`
    ).not.toBe("transparent");
    expect(
      appRootBg,
      `[${theme}] .app-root background-color should not be ELEMENT_MISSING`
    ).not.toBe("ELEMENT_MISSING");

    // Screenshot
    await page.screenshot({
      path: path.join(SCREENSHOT_DIR, `theme-${theme}.png`),
      fullPage: true,
    });
  });
}

test("all 9 --perch-bg values are distinct", async ({ browser }) => {
  // Each theme needs a fresh browser context (isolated init scripts)
  const bgByTheme: Record<string, string> = {};

  for (const theme of THEMES) {
    const context = await browser.newContext({ baseURL: `http://localhost:${PREVIEW_PORT}` });
    await context.addInitScript({ content: buildInitScriptContent({ settings: { theme } }) });
    const pg = await context.newPage();
    await pg.goto("/");
    await pg.waitForSelector("#app", { timeout: 10000 });
    await pg.waitForTimeout(1000);
    const bg = await pg.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--perch-bg").trim()
    );
    bgByTheme[theme] = bg;
    await context.close();
  }

  // Assert all values match expected map
  for (const [theme, expectedBg] of Object.entries(THEME_BG_MAP)) {
    expect(
      bgByTheme[theme],
      `Theme "${theme}" --perch-bg mismatch`
    ).toBe(expectedBg);
  }

  // Assert all 9 values are distinct (no duplicates)
  const values = Object.values(bgByTheme);
  const unique = new Set(values);
  expect(
    unique.size,
    `Expected 9 distinct --perch-bg values, got ${unique.size}: ${JSON.stringify(bgByTheme)}`
  ).toBe(THEMES.length);
});
