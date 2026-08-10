/**
 * screenshot-sweep.spec.ts
 *
 * This is a full visual sweep of the perch UI. Each test captures one or more
 * PNG screenshots to frontend/e2e/__screenshots__/sweep/.
 *
 * Design: pure navigation and screenshots, with no brittle assertions. The test
 * uses generous waits, so the state renders before it takes a screenshot. If a
 * state is unreachable, the test screenshots whatever is visible and continues.
 * It does not throw an error.
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent, WORKSPACE_FIXTURE, WORKSPACE_FIXTURE_NO_CAPS } from "./_mock";
import type { MockWorkspace } from "./_mock";
import { PREVIEW_PORT } from "../preview-port.mjs";
import { THEMES } from "../src/lib/constants";

// ── Output directory ─────────────────────────────────────────────────────────
const DIR = "./e2e/__screenshots__/sweep";

// ── Helpers ──────────────────────────────────────────────────────────────────

/** Screenshot shorthand. Writes the file to DIR/<name>.png. */
async function shot(page: import("@playwright/test").Page, name: string) {
  await page.screenshot({
    path: path.join(DIR, `${name}.png`),
    // A viewport shot works well for review. It avoids tall white tails on empty pages.
  });
}

/** Activate a session. Click its sidebar entry, then confirm the resume prompt. */
async function activateWorkspace(page: import("@playwright/test").Page, title = "test session") {
  const item = page.locator(`text=${title}`).first();
  const visible = await item.isVisible().catch(() => false);
  if (!visible) return;
  await item.click();
  await page.waitForTimeout(400);
  // The resume-preview gate. Confirm with the "Open" button, if present.
  const openBtn = page.locator('[data-testid="resume-preview"] button.btn-primary');
  const gateVisible = await openBtn.isVisible().catch(() => false);
  if (gateVisible) {
    await openBtn.click();
    await page.waitForTimeout(400);
  }
}

/** Open the command palette via the ":" keystroke. */
async function openPalette(page: import("@playwright/test").Page) {
  // First, dismiss any stray welcome-screen dialog. With no sessions, a click at
  // the center of .app-root lands on a welcome button. This opens a modal that
  // traps the keyboard, so ":" does nothing. Escape reaches a clean state.
  await page.keyboard.press("Escape");
  await page.keyboard.press(":");
  await page.waitForTimeout(400);
}

/** Open Settings via the command palette. */
async function openSettings(page: import("@playwright/test").Page) {
  await openPalette(page);
  await page.keyboard.type("settings");
  await page.waitForTimeout(200);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(400);
}

/** Emit a backend notification. */
async function emitNotification(
  page: import("@playwright/test").Page,
  tier: "blocking" | "ambient" | "routine",
  title: string,
  body: string,
  workspaceId = "ws-1",
) {
  await page.evaluate(
    ({ tier, title, body, workspaceId }) => {
      (window as any).__emit("notify", { tier, title, body, workspaceId });
    },
    { tier, title, body, workspaceId },
  );
  await page.waitForTimeout(300);
}

/** Build an init script that also overrides DiffStat to return files. */
function buildDiffInitScript(workspaces: MockWorkspace[]) {
  const base = buildInitScriptContent({ workspaces });

  // This patch injects serialised file and hunk data, and avoids template-escape issues.
  const files = JSON.stringify([
    { path: "src/app.go",          added: 42, removed: 8,  status: "M" },
    { path: "frontend/App.svelte", added: 17, removed: 3,  status: "M" },
    { path: "docs/CHANGELOG.md",   added: 5,  removed: 0,  status: "A" },
    { path: "old/legacy.ts",       added: 0,  removed: 61, status: "D" },
  ]);
  const hunks = JSON.stringify([
    {
      file: "__file__", index: 0,
      header: "@@ -10,7 +10,9 @@",
      oldStart: 10, oldLines: 7, newStart: 10, newLines: 9,
      lines: [
        { kind: "ctx", text: " func main() {" },
        { kind: "del", text: "-\tlog.Println(\"old startup\")" },
        { kind: "add", text: "+\tlog.Println(\"perch starting up\")" },
        { kind: "add", text: "+\tlog.Println(\"version: v1.0\")" },
        { kind: "ctx", text: " }" },
      ],
      staged: false,
    },
    {
      file: "__file__", index: 1,
      header: "@@ -55,4 +57,6 @@",
      oldStart: 55, oldLines: 4, newStart: 57, newLines: 6,
      lines: [
        { kind: "ctx", text: " // setup" },
        { kind: "add", text: "+\tconfig.Init()" },
        { kind: "add", text: "+\tconfig.Validate()" },
        { kind: "ctx", text: " }" },
      ],
      staged: false,
    },
  ]);

  const patch = `
(function() {
  var _files = ${files};
  var _hunks = ${hunks};
  window.go.app.App.DiffStat = function(worktree) {
    window.__calls.push({ method: 'DiffStat', args: [worktree] });
    return Promise.resolve(_files.slice());
  };
  window.go.app.App.Hunks = function(worktree, file) {
    window.__calls.push({ method: 'Hunks', args: [worktree, file] });
    var result = _hunks.map(function(h) {
      return Object.assign({}, h, { file: file });
    });
    return Promise.resolve(result);
  };
})();
`;
  return base + patch;
}

// ── Setup ────────────────────────────────────────────────────────────────────

test.beforeAll(() => {
  fs.mkdirSync(DIR, { recursive: true });
});

// Set a sensible desktop viewport for all tests in this file
test.use({ viewport: { width: 1440, height: 900 } });

// ── 1. Empty state ────────────────────────────────────────────────────────────

test("01-empty-state: no workspaces", async ({ browser }) => {
  const ctx = await browser.newContext({ baseURL: `http://localhost:${PREVIEW_PORT}`, viewport: { width: 1440, height: 900 } });
  await ctx.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await shot(page, "01-empty-state");
  await ctx.close();
});

// ── 2. Session list: various states in the sidebar ───────────────────────────

test("02-sidebar-workspace-states: idle / working / awaiting-input / awaiting-approval / errored", async ({ page }) => {
  const workspaces: MockWorkspace[] = [
    { ...WORKSPACE_FIXTURE, id: "ws-idle",     title: "idle session",      state: "idle",              branch: "main",      paneId: "pane-ws-idle" },
    { ...WORKSPACE_FIXTURE, id: "ws-running",  title: "working session",   state: "running",           branch: "feat/api",  paneId: "pane-ws-running" },
    { ...WORKSPACE_FIXTURE, id: "ws-awaiting", title: "awaiting input",    state: "awaiting-input",    branch: "fix/login", paneId: "pane-ws-awaiting" },
    { ...WORKSPACE_FIXTURE, id: "ws-approval", title: "needs approval",    state: "awaiting-approval", branch: "feat/db",   paneId: "pane-ws-approval" },
    { ...WORKSPACE_FIXTURE, id: "ws-errored",  title: "errored session",   state: "errored",           branch: "feat/err",  paneId: "pane-ws-errored" },
    { ...WORKSPACE_FIXTURE_NO_CAPS, id: "ws-done", title: "done session",  state: "done",              branch: "chore/cleanup", paneId: "pane-ws-done" },
  ];
  await page.addInitScript({ content: buildInitScriptContent({ workspaces }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await shot(page, "02-sidebar-workspace-states");
});

// ── 3. Agent terminal pane ───────────────────────────────────────────────────

test("03-agent-terminal-pane: active workspace, agent view", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  // Emit some pty data to make the terminal look populated
  await page.evaluate(() => {
    const bytes = Array.from(new TextEncoder().encode("$ perch running...\r\nAgent: Analyzing codebase...\r\n"));
    (window as any).__emit("pty:data:pane-ws-1", bytes);
  });
  await page.waitForTimeout(600);
  await shot(page, "03-agent-terminal-pane");
});

// ── 4. Awaiting-input signal ──────────────────────────────────────────────────

test("04-awaiting-input-signal: sidebar pulse + pane", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  // Transition to awaiting-input via agent:event
  await page.evaluate(() => {
    (window as any).__emit("agent:event", {
      workspaceId: "ws-1",
      kind: "state",
      state: "awaiting-input",
    });
  });
  await page.waitForTimeout(600);
  await shot(page, "04-awaiting-input-signal");
});

// ── 5. Approval card ──────────────────────────────────────────────────────────

test("05-approval-card: agent requests approval", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  await page.evaluate(() => {
    (window as any).__emit("agent:event", {
      workspaceId: "ws-1",
      kind: "approval",
      approval: {
        reqId: "req-sweep-1",
        tool: "bash",
        summary: "Run: npm install && npm run build",
        input: '{ "command": "npm install && npm run build" }',
      },
    });
  });
  await page.waitForTimeout(600);
  // Wait for the card to appear if possible
  const card = page.locator('section[aria-label="approval card"]');
  await card.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await shot(page, "05-approval-card");
});

// ── 6. Notification hub open ─────────────────────────────────────────────────

test("06-notification-hub: all three tiers", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await emitNotification(page, "blocking", "Approval Required", "bash wants to run npm ci", "ws-1");
  await emitNotification(page, "ambient",  "Task Complete",     "Agent finished refactor",  "ws-1");
  await emitNotification(page, "routine",  "File written",      "src/app.go was saved",     "ws-1");
  await emitNotification(page, "ambient",  "Branch pushed",     "feat/api pushed to remote","ws-1");
  // Open the notification hub with the bell button.
  const bell = page.locator('button[aria-label="notifications"]');
  await bell.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await bell.click().catch(() => {});
  await page.waitForTimeout(500);
  await shot(page, "06-notification-hub");
});

// ── 7. Diff view with hunks ───────────────────────────────────────────────────

test("07-diff-view-with-hunks: multi-file diff expanded", async ({ browser }) => {
  // Use a fresh browser context with a generous layout JSON, preset to diff view.
  // This skips the activate-and-resume flow that was causing hangs.
  const ctx = await browser.newContext({
    baseURL: `http://localhost:${PREVIEW_PORT}`,
    viewport: { width: 1440, height: 900 },
  });
  const layoutJSON = JSON.stringify({ view: "diff", split: false, sidebarW: 240, shellH: 200, collapsed: {} });
  const initScript = buildDiffInitScript([WORKSPACE_FIXTURE]);
  // This patch also overrides GetLayout to return the diff view. The
  // activateWorkspace call below sets activeId. It clicks the session in the
  // sidebar. That click triggers onSelect and confirms the resume preview.
  const layoutPatch = `
(function() {
  var _origLayout = window.go.app.App.GetLayout;
  window.go.app.App.GetLayout = function() {
    window.__calls.push({ method: 'GetLayout', args: [] });
    return Promise.resolve(${JSON.stringify(layoutJSON)});
  };
})();
`;
  await ctx.addInitScript({ content: initScript + layoutPatch });
  const page = await ctx.newPage();
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1000);

  // Activate the session (this triggers DiffStat): click it in the sidebar, then confirm the resume.
  await activateWorkspace(page);
  await page.waitForTimeout(800);

  // If the diff view rendered, file rows should be visible. Expand the first file.
  const firstFile = page.locator(".file-row").first();
  const fileVisible = await firstFile.isVisible().catch(() => false);
  if (fileVisible) {
    await firstFile.click().catch(() => {});
    await page.waitForTimeout(600);
  }
  await page.screenshot({ path: path.join(DIR, "07-diff-view-with-hunks.png") }).catch(() => {});
  await ctx.close();
});

// ── 8. Code / editor view ─────────────────────────────────────────────────────

test("08-code-editor-view: mock file loaded", async ({ page }) => {
  const initScript =
    buildInitScriptContent({
      workspaces: [WORKSPACE_FIXTURE],
      readFileContent: `// perch — agent-native cockpit\nimport { App } from "./app";\n\nconst app = new App();\nawait app.init();\nawait app.run();\n`,
    }) +
    `
(function() {
  window.go.app.App.ListDir = function(absDir) {
    window.__calls.push({ method: 'ListDir', args: [absDir] });
    return Promise.resolve([
      { name: 'app.ts',    path: '/home/user/project/app.ts',    isDir: false },
      { name: 'index.ts',  path: '/home/user/project/index.ts',  isDir: false },
      { name: 'utils.ts',  path: '/home/user/project/utils.ts',  isDir: false },
      { name: 'src',       path: '/home/user/project/src',       isDir: true  },
    ]);
  };
})();
`;
  await page.addInitScript({ content: initScript });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  const codeBtn = page.locator('nav[aria-label="View"] button', { hasText: "Code" });
  await codeBtn.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await codeBtn.click().catch(() => {});
  await page.waitForTimeout(500);
  // Click a file to open it in the editor
  const fileItem = page.locator("text=app.ts").first();
  await fileItem.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await fileItem.click().catch(() => {});
  await page.waitForTimeout(800);
  await shot(page, "08-code-editor-view");
});

// ── 9. Shell drawer open ──────────────────────────────────────────────────────

test("09-shell-drawer-open: Ctrl+` toggles shell", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  // Toggle the shell drawer with Ctrl+`.
  await page.locator(".app-root").click().catch(() => {});
  await page.keyboard.press("Control+`");
  await page.waitForTimeout(500);
  // Emit some shell output to make it look alive
  await page.evaluate(() => {
    const bytes = Array.from(new TextEncoder().encode("$ ls -la\r\ntotal 48\r\ndrwxr-xr-x  user user  4096 app.go\r\n"));
    (window as any).__emit("pty:data:shell-ws-1", bytes);
  });
  await page.waitForTimeout(500);
  await shot(page, "09-shell-drawer-open");
});

// ── 10. Settings panel ────────────────────────────────────────────────────────

test("10-settings-panel: open with always-rules", async ({ page }) => {
  await page.addInitScript({
    content: buildInitScriptContent({
      workspaces: [],
      settings: {
        theme: "gruvbox",
        density: "dense",
        font: "geist",
        dnd: false,
        alwaysRules: [
          { agent: "claude", tool: "bash",       pattern: "npm run *"   },
          { agent: "claude", tool: "write_file", pattern: "*.ts"        },
          { agent: "claude", tool: "read_file",  pattern: "src/**"      },
        ],
      },
    }),
  });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await openSettings(page);
  const panel = page.locator('[role="dialog"][aria-label="Settings"]');
  await panel.waitFor({ state: "visible", timeout: 4000 }).catch(() => {});
  await shot(page, "10-settings-panel");
});

// ── 11. Command palette ───────────────────────────────────────────────────────

test("11a-command-palette-open: full list", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await openPalette(page);
  const palette = page.locator('[role="dialog"][aria-label="command palette"]');
  await palette.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await shot(page, "11a-command-palette-open");
});

test("11b-command-palette-filtered: 'diff' query", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await openPalette(page);
  await page.keyboard.type("diff");
  await page.waitForTimeout(300);
  await shot(page, "11b-command-palette-filtered");
});

test("11c-command-palette-no-results: obscure query", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await openPalette(page);
  await page.keyboard.type("xyzzy-no-results-12345");
  await page.waitForTimeout(300);
  await shot(page, "11c-command-palette-no-results");
});

// ── 12. New-session dialog ────────────────────────────────────────────────────

test("12-new-session-dialog", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  // Open via command palette
  await openPalette(page);
  await page.keyboard.type("new session");
  await page.waitForTimeout(200);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(500);
  const dialog = page.locator('[role="dialog"]').filter({ hasText: /new session/i }).first();
  await dialog.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await shot(page, "12-new-session-dialog");
});

// ── 13. Help dialog ───────────────────────────────────────────────────────────

test("13-help-dialog: keyboard shortcuts", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  // Open via command palette → "Keyboard shortcuts"
  await openPalette(page);
  await page.keyboard.type("keyboard");
  await page.waitForTimeout(200);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(400);
  const dialog = page.locator('[role="dialog"][aria-label="help"]');
  await dialog.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await shot(page, "13-help-dialog");
});

// ── 14. Split-pane view ───────────────────────────────────────────────────────

test("14-split-pane-view: two agent terminals", async ({ page }) => {
  const ws2: MockWorkspace = { ...WORKSPACE_FIXTURE, id: "ws-2", title: "second session", branch: "feat/split", paneId: "pane-ws-2" };
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE, ws2] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  // Activate split
  const splitBtn = page.locator('button[aria-label="Split pane"]');
  await splitBtn.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await splitBtn.click().catch(() => {});
  await page.waitForTimeout(500);
  await shot(page, "14-split-pane-view");
});

// ── 15. Menu dropdown open ────────────────────────────────────────────────────

test("15-menu-dropdown-open: Session menu", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  const sessionBtn = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: "Session" });
  await sessionBtn.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await sessionBtn.click().catch(() => {});
  await page.waitForTimeout(300);
  await shot(page, "15-menu-dropdown-open");
});

// ── 16. Glass OFF variant ─────────────────────────────────────────────────────

test("16-glass-off: workspace view without glass effect", async ({ page }) => {
  // glassDisabled=true makes settings.glass=false, so data-glass="off".
  const initScript =
    buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) +
    `
(function() {
  var _origGet = window.go.app.App.GetSettings;
  window.go.app.App.GetSettings = function() {
    window.__calls.push({ method: 'GetSettings', args: [] });
    return Promise.resolve({ theme: 'gruvbox', density: 'dense', font: 'geist', dnd: false, glassDisabled: true, alwaysRules: [] });
  };
})();
`;
  await page.addInitScript({ content: initScript });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  await shot(page, "16-glass-off");
});

// ── 17. Glass ON (reference) ──────────────────────────────────────────────────

test("17-glass-on: workspace view with glass effect (reference)", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  await shot(page, "17-glass-on");
});

// ── 18. Theme sweep ───────────────────────────────────────────────────────────
// One test runs per theme. Each opens a populated view: sidebar, agent pane, and approval card.

for (const theme of THEMES) {
  test(`18-theme-${theme}`, async ({ browser }) => {
    const ctx = await browser.newContext({
      baseURL: `http://localhost:${PREVIEW_PORT}`,
      viewport: { width: 1440, height: 900 },
    });

    const initScript =
      buildInitScriptContent({
        settings: { theme, density: "dense", font: "geist", dnd: false },
        workspaces: [WORKSPACE_FIXTURE],
      });

    await ctx.addInitScript({ content: initScript });
    const page = await ctx.newPage();
    await page.goto("/");
    await page.waitForSelector("#app", { timeout: 10000 });
    await page.waitForTimeout(1200);
    await activateWorkspace(page);
    // Add approval card for visual richness
    await page.evaluate(() => {
      (window as any).__emit("agent:event", {
        workspaceId: "ws-1",
        kind: "approval",
        approval: { reqId: "req-theme", tool: "write_file", summary: "Write src/main.go" },
      });
    });
    await page.waitForTimeout(500);
    await page.screenshot({ path: path.join(DIR, `theme-${theme}.png`) });
    await ctx.close();
  });
}

// ── 19. Density variants ──────────────────────────────────────────────────────

for (const density of ["dense", "comfortable", "ultra"] as const) {
  test(`19-density-${density}`, async ({ browser }) => {
    const ctx = await browser.newContext({
      baseURL: `http://localhost:${PREVIEW_PORT}`,
      viewport: { width: 1440, height: 900 },
    });
    await ctx.addInitScript({
      content: buildInitScriptContent({
        settings: { theme: "gruvbox", density, font: "geist", dnd: false },
        workspaces: [WORKSPACE_FIXTURE],
      }),
    });
    const page = await ctx.newPage();
    await page.goto("/");
    await page.waitForSelector("#app", { timeout: 10000 });
    await page.waitForTimeout(1200);
    await activateWorkspace(page);
    await page.screenshot({ path: path.join(DIR, `19-density-${density}.png`) });
    await ctx.close();
  });
}

// ── 20. Sidebar collapsed ─────────────────────────────────────────────────────

test("20-sidebar-collapsed: Ctrl+B hides sidebar", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  await page.locator(".app-root").click().catch(() => {});
  await page.keyboard.press("Control+b");
  await page.waitForTimeout(400);
  await shot(page, "20-sidebar-collapsed");
});

// ── 21. DND active (notification hub with DND on) ────────────────────────────

test("21-dnd-active: notifications hub with do-not-disturb on", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  // Open hub
  const bell = page.locator('button[aria-label="notifications"]');
  await bell.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await bell.click().catch(() => {});
  await page.waitForTimeout(400);
  // Toggle DND on
  const dndBtn = page.locator('section[aria-label="notification hub"] button', { hasText: "Do not disturb" });
  await dndBtn.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await dndBtn.click().catch(() => {});
  await page.waitForTimeout(400);
  // Close and reopen the hub, to show the badge state.
  await bell.click().catch(() => {});
  await page.waitForTimeout(300);
  await bell.click().catch(() => {});
  await page.waitForTimeout(400);
  await shot(page, "21-dnd-active");
});

// ── 22. Diff view: no changes ─────────────────────────────────────────────────

test("22-diff-view-empty: no changes state", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  const diffBtn = page.locator('nav[aria-label="View"] button', { hasText: "Diff" });
  await diffBtn.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
  await diffBtn.click().catch(() => {});
  await page.waitForTimeout(500);
  await shot(page, "22-diff-view-empty");
});

// ── 23. Approval card then hub combo ─────────────────────────────────────────

test("23-approval-plus-notification-hub: both visible states", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await activateWorkspace(page);
  // Trigger an approval
  await page.evaluate(() => {
    (window as any).__emit("agent:event", {
      workspaceId: "ws-1",
      kind: "approval",
      approval: { reqId: "req-combo", tool: "bash", summary: "Run: git push origin HEAD" },
    });
  });
  await page.waitForTimeout(400);
  // Also emit a blocking notification, so the bell badge lights up.
  await emitNotification(page, "blocking", "Another Approval", "tool wants permission", "ws-1");
  await shot(page, "23-approval-plus-notification-hub");
});
