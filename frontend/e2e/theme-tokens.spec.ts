import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";

// This test checks for the CSS token regression described in the bug report.
// The Vite build graph must import tokens.css and themes.css.
// Only then do the --perch-* custom properties exist in the production bundle.
//
// Gate: check the semantic computed style, not a pixel diff.
//   1. --perch-bg on :root must not be empty. This proves tokens.css and themes.css reached the bundle.
//   2. The background-color of .app-root must not be transparent. This proves the app used the theme token.
//
// Note: the app body has no background rule. The .app-root element paints the background
// with var(--perch-bg). So the test queries that element, not document.body.

test.beforeEach(async ({ page }) => {
  // Mock the Go/Wails boundary before navigation. This lets the app boot.
  // Source: src/lib/wails.ts, window.go.app.App.<Method> and window.runtime.EventsOn.
  await page.addInitScript(() => {
    // runtime.EventsOn must return an unsubscribe function
    (window as any).runtime = {
      EventsOn: (_event: string, _cb: (...args: any[]) => void) => () => {},
    };

    // These App methods run during onMount: settings.load, layout.restore, and listWorkspaces.
    // The stubs also cover other calls the app might make before it reaches the "No session selected" state.
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
          // An empty string is falsy. It skips JSON.parse in layout.restore and keeps the defaults.
          GetLayout: async () => "",
          ListWorkspaces: async () => [],
          // Stub all other methods. This stops the app from throwing if another path calls them.
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

  // Wait for the Svelte app root to appear and the app to render.
  // The expected first paint is the "No session selected" empty state, with an empty session list.
  await page.waitForSelector("#app", { timeout: 10000 });
  // Give Svelte and onMount a moment to finish. settings.load and layout.restore are async.
  await page.waitForTimeout(1000);

  // Assertion 1: --perch-bg must be defined on :root. Non-empty means tokens.css reached the bundle.
  const percbBg = await page.evaluate(() =>
    getComputedStyle(document.documentElement)
      .getPropertyValue("--perch-bg")
      .trim()
  );

  // Assertion 2: the background-color of .app-root must not be transparent.
  // The .app-root div carries `background: var(--perch-bg)`. If the token is undefined,
  // the computed value falls back to the default transparent or black color.
  const appRootBg = await page.evaluate(() => {
    const el = document.querySelector(".app-root");
    if (!el) return "ELEMENT_MISSING";
    return getComputedStyle(el).backgroundColor;
  });

  // Capture a screenshot for visual evidence, whether the test passes or fails.
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
