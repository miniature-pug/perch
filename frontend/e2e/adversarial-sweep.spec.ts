/**
 * adversarial-sweep.spec.ts
 *
 * ADVERSARIAL non-happy-path sweep. Each test performs a WEIRD interaction
 * sequence, then asserts (softly) that:
 *   - the top-level error boundary / CrashScreen did NOT appear,
 *   - no uncaught page error / console.error fired during the run,
 *   - the app did not get stuck (the app-root is still interactive).
 *
 * Every step captures a screenshot to e2e/__screenshots__/adversarial/. Soft
 * assertions + per-step try/catch mean ONE run surfaces MANY issues instead of
 * bailing on the first failure.
 *
 * Harness reused from _mock.ts / the other specs: window.go stubs installed via
 * page.addInitScript, workspaces seeded, backend events fired with window.__emit.
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent, WORKSPACE_FIXTURE } from "./_mock";
import type { MockWorkspace, MockOptions } from "./_mock";
import { PREVIEW_PORT } from "../preview-port.mjs";
import { THEMES } from "../src/lib/constants";

const DIR = "./e2e/__screenshots__/adversarial";

type Page = import("@playwright/test").Page;

// ── Console / pageerror capture ──────────────────────────────────────────────
// One array of {test, msg} per page, wired in beforeEach. We ignore benign
// resource-load noise but keep genuine console.error and uncaught exceptions.
// The CSP `frame-ancestors ignored via <meta>` line is a benign Chromium
// warning emitted on every page load (the SPA ships CSP in a meta tag); it is
// not an app defect, so we filter it out to avoid drowning real errors.
const IGNORE_RE =
  /favicon|ERR_|net::|Failed to load resource|Download the .* DevTools|frame-ancestors' is ignored/i;

function attachConsoleCapture(page: Page, sink: string[]) {
  page.on("console", (m) => {
    if (m.type() === "error" && !IGNORE_RE.test(m.text())) {
      sink.push(`console.error: ${m.text()}`);
    }
  });
  page.on("pageerror", (e) => {
    sink.push(`pageerror: ${e.message}`);
  });
}

async function shot(page: Page, name: string) {
  await page.screenshot({ path: path.join(DIR, `${name}.png`) }).catch(() => {});
}

/** Soft-assert the crash screen is NOT present. Screenshots if it is. */
async function assertNoCrash(page: Page, label: string) {
  const crash = page.locator('[data-testid="crash-screen"]');
  const crashed = await crash.isVisible().catch(() => false);
  if (crashed) {
    await shot(page, `CRASH-${label}`);
  }
  expect.soft(crashed, `[${label}] CrashScreen must not be visible`).toBe(false);
}

/** Soft-assert the app-root is still there and the app is not frozen. */
async function assertAlive(page: Page, label: string) {
  const alive = await page.locator(".app-root").isVisible().catch(() => false);
  expect.soft(alive, `[${label}] .app-root must still be present (app not stuck)`).toBe(true);
}

// _mock.ts does not stub every IPC method the app can call (e.g.
// WorkspaceForBranch on the create path, SetWorkspaceTitle on rename,
// HomeShellCwd on the home shell). Patch the gaps so an adversarial path that
// exercises them surfaces REAL app behaviour instead of a mock-throw.
const MOCK_GAP_PATCH = `
(function() {
  var A = window.go && window.go.app && window.go.app.App;
  if (!A) return;
  function rec(m, args) { window.__calls.push({ method: m, args: args }); }
  if (!A.WorkspaceForBranch) A.WorkspaceForBranch = function(repo, branch) { rec('WorkspaceForBranch', [repo, branch]); return Promise.resolve({ id: '', found: false }); };
  if (!A.SetWorkspaceTitle) A.SetWorkspaceTitle = function(id, title) { rec('SetWorkspaceTitle', [id, title]); return Promise.resolve(); };
  if (!A.HomeShellCwd) A.HomeShellCwd = function() { rec('HomeShellCwd', []); return Promise.resolve('/home/user'); };
  if (!A.ForceRemoveWorkspace) A.ForceRemoveWorkspace = function(id) { rec('ForceRemoveWorkspace', [id]); return Promise.resolve(); };
})();
`;

async function boot(page: Page, opts: MockOptions, extra = "") {
  await page.addInitScript({ content: buildInitScriptContent(opts) + MOCK_GAP_PATCH + extra });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
}

/** Activate a workspace via sidebar click + resume-preview "Open". */
async function activate(page: Page, title = "test session") {
  const item = page.locator(`text=${title}`).first();
  if (!(await item.isVisible().catch(() => false))) return;
  await item.click();
  await page.waitForTimeout(350);
  const openBtn = page.locator('[data-testid="resume-preview"] button.btn-primary');
  if (await openBtn.isVisible().catch(() => false)) {
    await openBtn.click();
    await page.waitForTimeout(350);
  }
}

async function openPalette(page: Page) {
  // Dismiss any stray welcome-screen dialog first: with no workspaces, clicking
  // .app-root center lands on a welcome button and opens a modal that traps the
  // keyboard, making ":" a no-op. Escape reaches a clean state.
  await page.keyboard.press("Escape");
  await page.keyboard.press(":");
  await page.waitForTimeout(300);
}

/**
 * Open a dialog via the menubar (a NON-terminal zone). Reliable even when a
 * session is active — unlike the ":" palette, which the app swallows to the pty
 * while in TERMINAL mode. menuLabel e.g. "Session"/"Settings"/"Help",
 * itemLabel e.g. "New session"/"Settings…"/"Keyboard shortcuts".
 */
async function menuAction(page: Page, menuLabel: string, itemLabel: string) {
  const trigger = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: menuLabel }).first();
  await trigger.click().catch(() => {});
  await page.waitForTimeout(150);
  const item = page.locator('[role="menu"] [role="menuitem"]', { hasText: itemLabel }).first();
  await item.click().catch(() => {});
  await page.waitForTimeout(350);
}

async function paletteRun(page: Page, query: string) {
  await openPalette(page);
  await page.keyboard.type(query);
  await page.waitForTimeout(250);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(350);
}

async function emitNotify(
  page: Page,
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
  await page.waitForTimeout(200);
}

// ── Per-test wiring ──────────────────────────────────────────────────────────
const consoleErrors: string[] = [];

test.beforeAll(() => {
  fs.mkdirSync(DIR, { recursive: true });
});

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ page }) => {
  consoleErrors.length = 0;
  attachConsoleCapture(page, consoleErrors);
});

test.afterEach(async ({ page }, testInfo) => {
  // Fold any captured console errors into soft failures so the run continues
  // but the report shows them.
  if (consoleErrors.length) {
    await shot(page, `CONSOLE-ERR-${testInfo.title.replace(/[^a-z0-9]+/gi, "-").slice(0, 40)}`);
  }
  expect.soft(
    consoleErrors,
    `console errors / uncaught exceptions during "${testInfo.title}"`,
  ).toEqual([]);
});

// ═════════════════════════════════════════════════════════════════════════════
// 1. Rapid double-click Create in New Session
// ═════════════════════════════════════════════════════════════════════════════
test("A1-rapid-create-double-click", async ({ page }) => {
  await boot(page, { workspaces: [] });

  await test.step("open new-session dialog", async () => {
    await paletteRun(page, "new session");
    const dialog = page.locator('[role="dialog"][aria-label="new session"]');
    await dialog.waitFor({ state: "visible", timeout: 4000 }).catch(() => {});
    await shot(page, "A1-new-session-dialog");
  });

  await test.step("pick a repo so Create is enabled, then rapid-fire click", async () => {
    // Repo select is required for canCreate. Pick the first real option.
    const repo = page.locator('select[aria-label="repo"]');
    if (await repo.isVisible().catch(() => false)) {
      const opts = await repo.locator("option").allTextContents().catch(() => []);
      if (opts.length > 1) await repo.selectOption({ index: 1 }).catch(() => {});
    }
    await page.waitForTimeout(200);
    const create = page.locator('[role="dialog"][aria-label="new session"] button.btn-primary');
    // Fire five clicks as fast as possible with no awaits between.
    await Promise.all([
      create.click({ force: true }).catch(() => {}),
      create.click({ force: true }).catch(() => {}),
      create.click({ force: true }).catch(() => {}),
      create.click({ force: true }).catch(() => {}),
      create.click({ force: true }).catch(() => {}),
    ]);
    await page.waitForTimeout(600);
  });

  await test.step("verify at most ONE CreateWorkspace call fired (submitting guard)", async () => {
    const n = await page.evaluate(
      () => (window as any).__calls.filter((c: any) => c.method === "CreateWorkspace").length,
    );
    await shot(page, "A1-after-rapid-create");
    expect.soft(n, "rapid double-click must not fire CreateWorkspace more than once").toBeLessThanOrEqual(1);
  });

  await assertNoCrash(page, "A1");
  await assertAlive(page, "A1");
});

// ═════════════════════════════════════════════════════════════════════════════
// 2. Open session then fast view churn 1→2→3→1
// ═════════════════════════════════════════════════════════════════════════════
test("A2-fast-view-churn", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });
  await activate(page);

  await test.step("hammer number keys 1 2 3 1 with no waits", async () => {
    await page.locator(".app-root").click().catch(() => {});
    for (const k of ["1", "2", "3", "1", "3", "2", "1"]) {
      await page.keyboard.press(k);
    }
    await page.waitForTimeout(500);
    await shot(page, "A2-after-key-churn");
  });

  await test.step("hammer the segmented View buttons", async () => {
    const agent = page.locator('nav[aria-label="View"] button', { hasText: "Agent" });
    const code = page.locator('nav[aria-label="View"] button', { hasText: "Code" });
    const diff = page.locator('nav[aria-label="View"] button', { hasText: "Diff" });
    for (const b of [code, diff, agent, diff, code, agent]) {
      await b.click({ timeout: 1500 }).catch(() => {});
    }
    await page.waitForTimeout(400);
    // Exactly one button should be pressed at the end.
    const pressed = await page.locator('nav[aria-label="View"] button[aria-pressed="true"]').count();
    expect.soft(pressed, "exactly one view button pressed after churn").toBe(1);
    await shot(page, "A2-after-button-churn");
  });

  await assertNoCrash(page, "A2");
  await assertAlive(page, "A2");
});

// ═════════════════════════════════════════════════════════════════════════════
// 3. Resume-preview open → Escape → re-click same row
// ═════════════════════════════════════════════════════════════════════════════
test("A3-resume-preview-escape-reopen", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });

  const row = page.locator("text=test session").first();
  const preview = page.locator('[data-testid="resume-preview"]');

  await test.step("open preview, try to Escape it", async () => {
    await row.click();
    await page.waitForTimeout(300);
    await preview.waitFor({ state: "visible", timeout: 3000 }).catch(() => {});
    await shot(page, "A3-preview-open");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(300);
    const stillOpen = await preview.isVisible().catch(() => false);
    await shot(page, "A3-after-escape");
    // FINDING: the resume-preview has no Escape / backdrop-click handler, so
    // Escape does NOT dismiss it and its .modal-overlay keeps intercepting all
    // pointer events (the world is stuck until "Cancel" is clicked). This soft
    // assertion documents that gap.
    expect.soft(stillOpen, "resume-preview is NOT dismissable by Escape (bug)").toBe(false);
  });

  await test.step("dismiss via Cancel (the only working path), then re-click row", async () => {
    // Use Cancel so the overlay is actually cleared before re-clicking; a raw
    // row.click() here would hang 30s against the lingering modal-overlay.
    const cancel = preview.locator("button", { hasText: "Cancel" });
    if (await cancel.isVisible().catch(() => false)) {
      await cancel.click().catch(() => {});
      await page.waitForTimeout(300);
    }
    await row.click({ timeout: 4000 }).catch(() => {});
    await page.waitForTimeout(300);
    const reopened = await preview.isVisible().catch(() => false);
    await shot(page, "A3-preview-reopened");
    expect.soft(reopened, "re-clicking the same row should re-open the preview").toBe(true);
  });

  await assertNoCrash(page, "A3");
  await assertAlive(page, "A3");
});

// ═════════════════════════════════════════════════════════════════════════════
// 4. Agent → Code → Agent (terminal DOM persistence) + shell drawer toggle
// ═════════════════════════════════════════════════════════════════════════════
test("A4-view-roundtrip-and-shell-drawer", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });
  await activate(page);

  await test.step("seed terminal output, then Agent→Code→Agent", async () => {
    await page.evaluate(() => {
      const bytes = Array.from(new TextEncoder().encode("$ hello from pty\r\nline two\r\n"));
      (window as any).__emit("pty:data:pane-ws-1", bytes);
    });
    await page.waitForTimeout(300);
    await page.locator('nav[aria-label="View"] button', { hasText: "Code" }).click().catch(() => {});
    await page.waitForTimeout(300);
    await page.locator('nav[aria-label="View"] button', { hasText: "Agent" }).click().catch(() => {});
    await page.waitForTimeout(400);
    await shot(page, "A4-back-to-agent");
    // The agent terminal pane should be present again.
    const pane = await page.locator('[data-pane="primary"]').isVisible().catch(() => false);
    expect.soft(pane, "primary agent pane should render after Code→Agent roundtrip").toBe(true);
  });

  await test.step("toggle shell drawer via Ctrl+` twice", async () => {
    // With a session active there are two `.shell-drawer` nodes in the DOM (a
    // hidden home-shell one and the visible session one); scope to the visible.
    await page.locator(".app-root").click().catch(() => {});
    await page.keyboard.press("Control+`");
    await page.waitForTimeout(400);
    await shot(page, "A4-shell-open");
    const drawerVisible = await page.locator(".shell-drawer:visible").first().isVisible().catch(() => false);
    expect.soft(drawerVisible, "a shell drawer should be visible in the DOM").toBe(true);
    await page.keyboard.press("Control+`");
    await page.waitForTimeout(400);
    await shot(page, "A4-shell-closed");
  });

  await assertNoCrash(page, "A4");
  await assertAlive(page, "A4");
});

// ═════════════════════════════════════════════════════════════════════════════
// 5. Approval on a BACKGROUND (non-active) session → pulse → open → allow
// ═════════════════════════════════════════════════════════════════════════════
test("A5-background-approval-pulse", async ({ page }) => {
  const wsA: MockWorkspace = { ...WORKSPACE_FIXTURE, id: "ws-1", title: "active one", paneId: "pane-ws-1" };
  const wsB: MockWorkspace = { ...WORKSPACE_FIXTURE, id: "ws-2", title: "background one", branch: "feat/bg", paneId: "pane-ws-2" };
  await boot(page, { workspaces: [wsA, wsB] });
  await activate(page, "active one");

  await test.step("fire approval on the NON-active ws-2", async () => {
    await page.evaluate(() => {
      (window as any).__emit("agent:event", {
        workspaceId: "ws-2",
        kind: "approval",
        approval: { reqId: "req-bg-1", tool: "bash", summary: "Run: rm -rf /tmp/scratch" },
      });
    });
    await page.waitForTimeout(500);
    await shot(page, "A5-sidebar-pulse");
    // The background row should carry an attention/approval signal class.
    const bgRow = page.locator("text=background one").first();
    expect.soft(await bgRow.isVisible().catch(() => false), "background row still visible").toBe(true);
  });

  await test.step("switch to ws-2 and confirm resume, then screenshot approval card", async () => {
    await activate(page, "background one");
    await page.waitForTimeout(400);
    const card = page.locator('section[aria-label="approval card"]');
    const cardVisible = await card.isVisible().catch(() => false);
    await shot(page, "A5-approval-card");
    expect.soft(cardVisible, "approval card should show for ws-2 after opening it").toBe(true);
    if (cardVisible) {
      await card.getByRole("button", { name: "Allow", exact: true }).click().catch(() => {});
      await page.waitForTimeout(400);
      const gone = await card.isVisible().catch(() => false);
      await shot(page, "A5-after-allow");
      expect.soft(gone, "approval card should clear after Allow").toBe(false);
    }
  });

  await assertNoCrash(page, "A5");
  await assertAlive(page, "A5");
});

// ═════════════════════════════════════════════════════════════════════════════
// 6. Notification storm → hub → click item → click item for a removed ws
// ═════════════════════════════════════════════════════════════════════════════
test("A6-notification-storm-and-navigation", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });

  await test.step("fire a storm of notifications", async () => {
    await emitNotify(page, "blocking", "Approval A", "bash wants in", "ws-1");
    await emitNotify(page, "ambient", "Ambient B", "task done", "ws-1");
    await emitNotify(page, "routine", "Routine C", "file saved", "ws-1");
    await emitNotify(page, "blocking", "Approval D", "orphan target", "ws-does-not-exist");
    await emitNotify(page, "ambient", "Ambient E", "no ws", "");
    await page.waitForTimeout(200);
  });

  await test.step("open hub, screenshot", async () => {
    await page.locator('button[aria-label="notifications"]').click().catch(() => {});
    await page.waitForTimeout(400);
    await shot(page, "A6-hub-open");
    const hub = page.locator('section[aria-label="notification hub"]');
    expect.soft(await hub.isVisible().catch(() => false), "notification hub should open").toBe(true);
  });

  await test.step("click a notification bound to a REAL ws → navigation", async () => {
    const navBtn = page.locator("button.notif-nav").filter({ hasText: "Approval A" }).first();
    if (await navBtn.isVisible().catch(() => false)) {
      await navBtn.click().catch(() => {});
      await page.waitForTimeout(400);
    }
    await shot(page, "A6-after-real-nav");
  });

  await test.step("click a notification whose workspace was REMOVED / never existed", async () => {
    await page.locator('button[aria-label="notifications"]').click().catch(() => {});
    await page.waitForTimeout(300);
    const orphan = page.locator("button.notif-nav").filter({ hasText: "Approval D" }).first();
    if (await orphan.isVisible().catch(() => false)) {
      await orphan.click().catch(() => {});
      await page.waitForTimeout(500);
    }
    await shot(page, "A6-after-orphan-nav");
    // Navigating to a nonexistent ws must not crash / freeze.
  });

  await assertNoCrash(page, "A6");
  await assertAlive(page, "A6");
});

// ═════════════════════════════════════════════════════════════════════════════
// 7. Rename via double-click and via right-click; commit + Escape-cancel
// ═════════════════════════════════════════════════════════════════════════════
test("A7-rename-session-dblclick-and-contextmenu", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });
  // Open the session first: on a CLOSED row the first click of a double-click
  // fires the row-select → resume-preview gate, which shadows rename. Renaming
  // an OPEN session is the realistic path and avoids that overlay.
  await activate(page);

  const title = page.locator("span.workspace-title").filter({ hasText: "test session" }).first();
  const input = page.locator("input.workspace-title-edit");

  await test.step("double-click → inline input, type new name, Enter commits", async () => {
    await title.dblclick({ timeout: 4000 }).catch(() => {});
    await page.waitForTimeout(250);
    const shown = await input.isVisible().catch(() => false);
    await shot(page, "A7-inline-input-dblclick");
    expect.soft(shown, "double-click should open inline rename input").toBe(true);
    if (shown) {
      await input.fill("renamed via dblclick");
      await input.press("Enter");
      await page.waitForTimeout(300);
    }
    await shot(page, "A7-after-commit");
  });

  await test.step("right-click → context menu (Rename + Remove); Rename opens inline input, Escape cancels", async () => {
    // F37 redesign: right-click no longer jumps straight into inline rename. It
    // opens a Rename/Remove context menu (mirrors the FileTree context-menu
    // pattern); the inline input is reached by choosing Rename from that menu.
    const t2 = page.locator("span.workspace-title").first();
    await t2.click({ button: "right", timeout: 4000 }).catch(() => {});
    await page.waitForTimeout(250);

    const menu = page.getByRole("menu", { name: "Session actions" });
    const menuShown = await menu.isVisible().catch(() => false);
    await shot(page, "A7-contextmenu-open");
    expect.soft(menuShown, "right-click should open the Rename/Remove context menu").toBe(true);

    const renameItem = menu.getByRole("menuitem", { name: "Rename" });
    const removeItem = menu.getByRole("menuitem", { name: "Remove" });
    expect.soft(await renameItem.isVisible().catch(() => false), "context menu offers Rename").toBe(true);
    expect.soft(await removeItem.isVisible().catch(() => false), "context menu offers Remove").toBe(true);

    // Choosing Rename enters inline-rename mode (the input replaces the title span).
    if (menuShown) {
      await renameItem.click().catch(() => {});
      await page.waitForTimeout(250);
    }
    const shown = await input.isVisible().catch(() => false);
    await shot(page, "A7-inline-input-contextmenu");
    expect.soft(shown, "choosing Rename should open the inline rename input").toBe(true);
    if (shown) {
      await input.fill("SHOULD NOT STICK");
      await input.press("Escape");
      await page.waitForTimeout(300);
      const stillEditing = await input.isVisible().catch(() => false);
      expect.soft(stillEditing, "Escape should close the rename input").toBe(false);
    }
    await shot(page, "A7-after-escape-cancel");
  });

  await assertNoCrash(page, "A7");
  await assertAlive(page, "A7");
});

// ═════════════════════════════════════════════════════════════════════════════
// 8. Split stage → pick → repick → toggle off
// ═════════════════════════════════════════════════════════════════════════════
test("A8-split-pick-repick-toggle", async ({ page }) => {
  const ws2: MockWorkspace = { ...WORKSPACE_FIXTURE, id: "ws-2", title: "second session", branch: "feat/2", paneId: "pane-ws-2" };
  const ws3: MockWorkspace = { ...WORKSPACE_FIXTURE, id: "ws-3", title: "third session", branch: "feat/3", paneId: "pane-ws-3" };
  await boot(page, { workspaces: [WORKSPACE_FIXTURE, ws2, ws3] });
  await activate(page);

  const splitBtn = page.locator('button[aria-label="Split pane"]');
  const picker = page.locator('[data-testid="split-picker"]');
  const select = page.locator("select.split-picker-select");

  await test.step("turn split ON → picker appears", async () => {
    await splitBtn.click().catch(() => {});
    await page.waitForTimeout(400);
    await shot(page, "A8-split-on-picker");
    expect.soft(await picker.isVisible().catch(() => false), "split picker should appear when secondary empty").toBe(true);
  });

  await test.step("pick a session for secondary", async () => {
    if (await select.isVisible().catch(() => false)) {
      await select.selectOption({ index: 1 }).catch(() => {});
      await page.waitForTimeout(400);
    }
    await shot(page, "A8-split-picked");
    expect.soft(await page.locator('[data-pane="secondary"]').isVisible().catch(() => false), "secondary pane fills after pick").toBe(true);
  });

  await test.step("repick a DIFFERENT session (remount path)", async () => {
    // Re-open picker if the app hid it; else the select may still be reachable.
    if (await select.isVisible().catch(() => false)) {
      const optCount = await select.locator("option").count();
      if (optCount > 2) await select.selectOption({ index: 2 }).catch(() => {});
      await page.waitForTimeout(400);
    }
    await shot(page, "A8-split-repicked");
  });

  await test.step("toggle split OFF", async () => {
    await splitBtn.click().catch(() => {});
    await page.waitForTimeout(400);
    await shot(page, "A8-split-off");
    expect.soft(await page.locator('[data-pane="secondary"]').isVisible().catch(() => false), "secondary gone after toggle off").toBe(false);
  });

  await assertNoCrash(page, "A8");
  await assertAlive(page, "A8");
});

// ═════════════════════════════════════════════════════════════════════════════
// 9. Open every dialog and Escape each; dialog + notification stacking
// ═════════════════════════════════════════════════════════════════════════════
test("A9-dialog-escape-and-stacking", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });

  const escAndCheck = async (label: string, dialogSel: string) => {
    const d = page.locator(dialogSel).first();
    const opened = await d.isVisible().catch(() => false);
    await shot(page, `A9-${label}-open`);
    expect.soft(opened, `${label} dialog should open`).toBe(true);
    await page.keyboard.press("Escape");
    await page.waitForTimeout(300);
    const closed = !(await d.isVisible().catch(() => false));
    expect.soft(closed, `${label} dialog should close on Escape`).toBe(true);
  };

  // Open via the menubar (a non-terminal zone) so dialogs open reliably.
  await test.step("New Session dialog", async () => {
    await menuAction(page, "Session", "New session");
    await escAndCheck("new-session", '[role="dialog"][aria-label="new session"]');
  });

  await test.step("Settings dialog", async () => {
    await menuAction(page, "Settings", "Settings");
    await escAndCheck("settings", '[role="dialog"][aria-label="Settings"]');
  });

  await test.step("Help / keyboard shortcuts dialog", async () => {
    await menuAction(page, "Help", "Keyboard shortcuts");
    await escAndCheck("help", '[role="dialog"][aria-label="help"]');
  });

  await test.step("Confirm-remove dialog (needs active ws)", async () => {
    await activate(page);
    await menuAction(page, "Session", "Remove session");
    await escAndCheck("confirm-remove", '[role="dialog"][aria-label="confirm"]');
  });

  await test.step("dialog + notification stacking", async () => {
    await menuAction(page, "Settings", "Settings");
    await emitNotify(page, "blocking", "Stack Test", "notification over a dialog", "ws-1");
    await page.waitForTimeout(300);
    await shot(page, "A9-dialog-plus-notification-stack");
    await assertNoCrash(page, "A9-stack");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
  });

  await assertNoCrash(page, "A9");
  await assertAlive(page, "A9");
});

// ═════════════════════════════════════════════════════════════════════════════
// 9b. Cleanup dialog (seeded stale sessions — requires ListStaleSessions mock)
// ═════════════════════════════════════════════════════════════════════════════
test("A9b-cleanup-dialog", async ({ page }) => {
  const stale = JSON.stringify([
    { id: "ws-stale-1", title: "stale one", branch: "feat/old", agent: "claude", lastActive: new Date(Date.now() - 9e8).toISOString(), added: 0, removed: 0, clean: true, merged: true, safe: true },
    { id: "ws-stale-2", title: "stale two", branch: "feat/older", agent: "claude", lastActive: new Date(Date.now() - 9e8).toISOString(), added: 12, removed: 3, clean: false, merged: false, safe: false },
  ]);
  const script =
    buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }) +
    `
(function() {
  var _stale = ${stale};
  window.go.app.App.ListStaleSessions = function() {
    window.__calls.push({ method: 'ListStaleSessions', args: [] });
    return Promise.resolve(_stale.slice());
  };
  window.go.app.App.CleanupSessions = function(ids, force) {
    window.__calls.push({ method: 'CleanupSessions', args: [ids, force] });
    _stale = [];
    return Promise.resolve();
  };
  window.go.app.App.ForceRemoveWorkspace = function(id) {
    window.__calls.push({ method: 'ForceRemoveWorkspace', args: [id] });
    return Promise.resolve();
  };
})();
`;
  await page.addInitScript({ content: script });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  await test.step("stale banner shows, click Review → cleanup panel", async () => {
    const banner = page.locator('[data-testid="stale-banner"]');
    const bShown = await banner.isVisible().catch(() => false);
    await shot(page, "A9b-stale-banner");
    expect.soft(bShown, "stale banner should appear when ListStaleSessions returns rows").toBe(true);
    if (bShown) {
      await banner.locator("button.stale-banner-link").click().catch(() => {});
      await page.waitForTimeout(400);
    }
    const panel = page.locator('[role="dialog"][aria-label="Stale session cleanup"]');
    await shot(page, "A9b-cleanup-open");
    expect.soft(await panel.isVisible().catch(() => false), "cleanup panel should open").toBe(true);
  });

  await test.step("try Escape to close cleanup panel", async () => {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(300);
    const panel = page.locator('[role="dialog"][aria-label="Stale session cleanup"]');
    const closed = !(await panel.isVisible().catch(() => false));
    await shot(page, "A9b-after-escape");
    // CleanupPanel self-closes on Escape, consistent with every other dialog
    // (New Session / Settings / Help / Confirm).
    expect.soft(closed, "cleanup panel should close on Escape (consistent w/ other dialogs)").toBe(true);
    // Fallback: if Escape did NOT close it, close via the X so the app is not
    // left stuck for assertAlive. When Escape already closed it, skip this —
    // otherwise the ✕ locator matches nothing and polls until the test timeout.
    if (!closed) {
      await page.locator('[role="dialog"][aria-label="Stale session cleanup"] button', { hasText: "✕" }).first().click().catch(() => {});
      await page.waitForTimeout(200);
    }
  });

  await assertNoCrash(page, "A9b");
  await assertAlive(page, "A9b");
});

// ═════════════════════════════════════════════════════════════════════════════
// 10. Empty states
// ═════════════════════════════════════════════════════════════════════════════
test("A10-empty-no-sessions", async ({ browser }) => {
  const ctx = await browser.newContext({ baseURL: `http://localhost:${PREVIEW_PORT}`, viewport: { width: 1440, height: 900 } });
  const sink: string[] = [];
  await ctx.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  const page = await ctx.newPage();
  attachConsoleCapture(page, sink);
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1200);
  await shot(page, "A10-empty-state");
  const empty = page.locator('[data-testid="empty-state"]');
  expect.soft(await empty.isVisible().catch(() => false), "empty-state renders with no sessions").toBe(true);
  await assertNoCrash(page, "A10");
  expect.soft(sink, "console errors in empty state").toEqual([]);
  await ctx.close();
});

test("A10b-diff-view-no-changes", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });
  await activate(page);
  await page.locator('nav[aria-label="View"] button', { hasText: "Diff" }).click().catch(() => {});
  await page.waitForTimeout(500);
  await shot(page, "A10b-diff-empty");
  await assertNoCrash(page, "A10b");
  await assertAlive(page, "A10b");
});

// ═════════════════════════════════════════════════════════════════════════════
// 11. Theme + density rapid switching
// ═════════════════════════════════════════════════════════════════════════════
test("A11-theme-density-rapid-switch", async ({ page }) => {
  await boot(page, { workspaces: [WORKSPACE_FIXTURE] });
  await activate(page);

  await test.step("rapidly cycle every theme via SaveSettings-backed select", async () => {
    await menuAction(page, "Settings", "Settings");
    const themeSel = page.locator('select[aria-label="Theme"]');
    const densitySel = page.locator('select[aria-label="Density"]');
    if (await themeSel.isVisible().catch(() => false)) {
      for (const t of THEMES) {
        await themeSel.selectOption(t).catch(() => {});
        await page.waitForTimeout(60);
      }
    }
    // grab a couple of screenshots mid-flight
    await shot(page, "A11-theme-last");
    if (await densitySel.isVisible().catch(() => false)) {
      for (const d of ["dense", "comfortable", "ultra", "dense"]) {
        await densitySel.selectOption(d).catch(() => {});
        await page.waitForTimeout(60);
      }
    }
    await shot(page, "A11-density-last");
    const dt = await page.evaluate(() => document.documentElement.getAttribute("data-theme"));
    expect.soft(dt, "data-theme should reflect the last selected theme").toBe(THEMES[THEMES.length - 1]);
  });

  await assertNoCrash(page, "A11");
  await assertAlive(page, "A11");
});
