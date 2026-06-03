/**
 * (i) editor.spec.ts
 *
 * Mock readFile + hunks (git gutter); assert .perch-git-gutter renders;
 * Ctrl-S → assert WriteFile called once with edited content.
 *
 * The Editor only mounts when layout.view === "code" AND a codePath is set.
 * codePath is set by clicking a file in the FileTree.
 * Since we can't click a real file (ListDir returns [] by default), we instead:
 *   1. Override ListDir to return a mock file entry so FileTree renders it
 *   2. Click the mock file to set codePath → Editor mounts
 *   3. Assert ReadFile and Hunks were called (editor loaded)
 *   4. Assert .perch-git-gutter is rendered (hunks not empty)
 *   5. Ctrl-S → WriteFile called with content
 *
 * IPC methods: ReadFile, Hunks, WriteFile
 * role="region" name="editor" → <section aria-label="editor">
 * gutter class: .perch-git-gutter (from Editor.svelte: gutter({ class: "perch-git-gutter", ... }))
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent, WORKSPACE_FIXTURE } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";
const MOCK_FILE_PATH = `${WORKSPACE_FIXTURE.worktreePath}/main.js`;
const MOCK_FILE_CONTENT = "// hello perch\nconsole.log('world');\n";

// Hunks that describe a change on line 1
const MOCK_HUNKS = [
  {
    file: MOCK_FILE_PATH,
    index: 0,
    header: "@@ -1,1 +1,2 @@",
    oldStart: 1, oldLines: 1, newStart: 1, newLines: 2,
    lines: [
      { kind: "add", text: "// hello perch" },
      { kind: "ctx", text: "console.log('world');" },
    ],
  },
];

// We need ListDir to return a file so the FileTree shows it.
// Build a custom init script that also overrides ListDir.
function buildEditorInitScript() {
  // We embed a special ListDir that returns one file for the worktree root
  const base = buildInitScriptContent({
    workspaces: [WORKSPACE_FIXTURE],
    readFileContent: MOCK_FILE_CONTENT,
    hunks: MOCK_HUNKS,
  });

  // Append ListDir override after the base script
  const override = `
(function() {
  var _orig = window.go.app.App.ListDir;
  window.go.app.App.ListDir = function(absDir) {
    window.__calls.push({ method: 'ListDir', args: [absDir] });
    return Promise.resolve([
      { name: 'main.js', path: ${JSON.stringify(MOCK_FILE_PATH)}, isDir: false }
    ]);
  };
})();
`;
  return base + "\n" + override;
}

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ content: buildEditorInitScript() });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  // Activate the workspace
  const sidebarItem = page.locator("text=test session").first();
  if (await sidebarItem.isVisible()) {
    await sidebarItem.click();
    await page.waitForTimeout(500);
  }

  // Switch to code view
  const codeBtn = page.locator('nav[aria-label="View"] button', { hasText: "Code" });
  await codeBtn.click();
  await page.waitForTimeout(500);
});

test("code view renders FileTree and clicking file loads Editor", async ({ page }) => {
  // FileTree should have rendered with the mock file
  const fileItem = page.locator("text=main.js");
  await expect(fileItem).toBeVisible({ timeout: 5000 });

  // Click the file to open it in the Editor
  await fileItem.click();
  await page.waitForTimeout(800);

  // Editor region should be present
  const editorRegion = page.locator('section[aria-label="editor"]');
  await expect(editorRegion).toBeVisible({ timeout: 5000 });

  // ReadFile should have been called
  const readFileCalls = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "ReadFile")
  );
  expect(readFileCalls.length, "ReadFile should have been called").toBeGreaterThan(0);
  expect(readFileCalls[readFileCalls.length - 1].args[0]).toBe(MOCK_FILE_PATH);

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "editor-loaded.png"), fullPage: true });
});

test("git gutter renders for a file with hunks", async ({ page }) => {
  const fileItem = page.locator("text=main.js");
  await expect(fileItem).toBeVisible({ timeout: 5000 });
  await fileItem.click();
  await page.waitForTimeout(1000);

  // .perch-git-gutter class from Editor.svelte gutter({ class: "perch-git-gutter" })
  const gutter = page.locator(".perch-git-gutter");
  await expect(gutter, ".perch-git-gutter gutter element should be present when hunks returned").toBeVisible({ timeout: 5000 });

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "editor-git-gutter.png"), fullPage: true });
});

test("Ctrl-S calls WriteFile with the current content", async ({ page }) => {
  const fileItem = page.locator("text=main.js");
  await expect(fileItem).toBeVisible({ timeout: 5000 });
  await fileItem.click();
  await page.waitForTimeout(1000);

  // Ensure editor is mounted
  await expect(page.locator('section[aria-label="editor"]')).toBeVisible({ timeout: 5000 });

  const writeCallsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "WriteFile").length
  );

  // Press Ctrl-S to trigger save
  await page.keyboard.press("Control+s");
  await page.waitForTimeout(500);

  const writeCallsAfter = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "WriteFile")
  );
  expect(
    writeCallsAfter.length - writeCallsBefore,
    "WriteFile should be called exactly once after Ctrl-S"
  ).toBe(1);

  const lastCall = writeCallsAfter[writeCallsAfter.length - 1];
  expect(lastCall.args[0], "WriteFile path should match").toBe(MOCK_FILE_PATH);
  expect(typeof lastCall.args[1], "WriteFile content should be a string").toBe("string");
  expect(
    (lastCall.args[1] as string).length,
    "WriteFile content should be non-empty"
  ).toBeGreaterThan(0);

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "editor-after-save.png"), fullPage: true });
});

test("Editor renders nothing when no file is selected", async ({ page }) => {
  // In code view without clicking a file, no editor region
  await expect(page.locator('section[aria-label="editor"]')).not.toBeVisible();
});
