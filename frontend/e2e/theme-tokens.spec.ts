import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";

// This test guards against the CSS token regression described in the bug report:
// tokens.css / themes.css must be imported into the Vite build graph so that
// --perch-* custom properties are defined in the production bundle.
//
// Gate: assert SEMANTIC computed-style, not a pixel diff:
//   1. --perch-bg on :root must be non-empty  (proves tokens.css / themes.css reached the bundle)
//   2. .app-root background-color must not be transparent (proves the theme token was consumed)
//
// Note: body has no background rule in this app — the background is painted by .app-root
// via var(--perch-bg), so we query that element, not document.body.

test.beforeEach(async ({ page }) => {
  // Mock the Go/Wails boundary BEFORE navigation so the app can boot.
  // Derived from src/lib/wails.ts: window.go.app.App.<Method> + window.runtime.EventsOn
  await page.addInitScript(() => {
    // runtime.EventsOn must return an unsubscribe function
    (window as any).runtime = {
      EventsOn: (_event: string, _cb: (...args: any[]) => void) => () => {},
    };

    // App methods called during onMount (settings.load, layout.restore, listWorkspaces)
    // plus stubs for anything the app might call before reaching "No session selected" state.
    (window as any).go = {
      app: {
        App: {
          GetSettings: async () => ({
            theme: "gruvbox",
            density: "dense",
            font: "geist",
            dnd: false,
            alwaysRules: [],
          }),
          // Empty string → falsy → skips JSON.parse in layout.restore, keeps defaults
          GetLayout: async () => "",
          ListWorkspaces: async () => [],
          // Stub all other methods so the app won't throw if any other path invokes them
          CreateWorkspace: async () => ({}),
          OpenWorkspace: async () => {},
          CloseWorkspace: async () => {},
          RemoveWorkspace: async () => {},
          WriteToPty: async () => {},
          ResizePty: async () => {},
          OpenShell: async () => {},
          Approve: async () => {},
          DiffStat: async () => [],
          Hunks: async () => [],
          StageHunk: async () => {},
          DiscardHunk: async () => {},
          ListDir: async () => [],
          ReadFile: async () => "",
          WriteFile: async () => {},
          RevealInFiles: async () => {},
          CopyPath: async () => {},
          Branches: async () => [],
          Worktrees: async () => [],
          SaveLayout: async () => {},
          SaveSettings: async () => {},
        },
      },
    };
  });
});

test("theme tokens resolve in built bundle", async ({ page }) => {
  await page.goto("/");

  // Wait for the Svelte app root to be present and the app to have rendered.
  // "No session selected" empty-state is the expected first-paint with empty workspace list.
  await page.waitForSelector("#app", { timeout: 10000 });
  // Give Svelte/onMount a moment to complete (settings.load + layout.restore are async)
  await page.waitForTimeout(1000);

  // Assertion 1: --perch-bg must be defined on :root (non-empty = tokens.css reached bundle)
  const percbBg = await page.evaluate(() =>
    getComputedStyle(document.documentElement)
      .getPropertyValue("--perch-bg")
      .trim()
  );

  // Assertion 2: .app-root background-color must not be transparent.
  // The .app-root div carries `background: var(--perch-bg)` — if the token is undefined
  // the computed value degrades to the default transparent/black.
  const appRootBg = await page.evaluate(() => {
    const el = document.querySelector(".app-root");
    if (!el) return "ELEMENT_MISSING";
    return getComputedStyle(el).backgroundColor;
  });

  // Capture screenshot for visual evidence (regardless of pass/fail)
  const screenshotsDir = "./e2e/__screenshots__";
  fs.mkdirSync(screenshotsDir, { recursive: true });
  await page.screenshot({
    path: path.join(screenshotsDir, "first-paint.png"),
    fullPage: true,
  });

  // The discriminating assertions
  expect(
    percbBg,
    `--perch-bg on :root is empty — tokens.css/themes.css are not in the bundle. Got: ${JSON.stringify(percbBg)}`
  ).not.toBe("");

  expect(
    appRootBg,
    `--perch-bg is defined (${percbBg}) but .app-root background-color is still transparent — token not applied to element`
  ).not.toBe("rgba(0, 0, 0, 0)");

  expect(
    appRootBg,
    `.app-root background-color should not be transparent (got: ${appRootBg})`
  ).not.toBe("transparent");
});
