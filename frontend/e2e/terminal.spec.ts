/**
 * (g) terminal.spec.ts
 *
 * Render a pane with Terminal; emit pty:data:<paneId> event; assert xterm renders
 * the text in the DOM.
 *
 * Note: xterm.js without a canvas/WebGL addon uses a DOM renderer that writes
 * text into .xterm-rows span elements. We assert the text appears there.
 * The event channel for pty data is: "pty:data:" + paneId  (from wails.ts onPtyData)
 * The event payload is number[] (byte array) — wails.ts: Uint8Array.from(data)
 *
 * We cannot assert the text is in visible rows without a real viewport, so we
 * assert the write was received by verifying xterm's internal state via the
 * terminal DOM or by asserting that WriteToPty was NOT called (since we only
 * emit from backend → frontend, not the reverse), and that the xterm container
 * has rendered at least some row content.
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent, WORKSPACE_FIXTURE } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";
const PANE_ID = WORKSPACE_FIXTURE.paneId; // "pane-ws-1"
const TEST_TEXT = "hello perch terminal";

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript({
    content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }),
  });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  // Activate the workspace so Terminal mounts
  const sidebarItem = page.locator("text=test session").first();
  if (await sidebarItem.isVisible()) {
    await sidebarItem.click();
    await page.waitForTimeout(500);
    // Resume preview now gates session open — click "Open" to confirm.
    const resumeOpenBtn = page.locator('[data-testid="resume-preview"] button.btn-primary');
    await resumeOpenBtn.waitFor({ state: "visible", timeout: 5000 });
    await resumeOpenBtn.click();
    await page.waitForTimeout(500);
  }

  // Agent view (default) renders Terminal in primary pane
  // Wait for xterm to mount (it mounts in onMount)
  await page.waitForSelector(".xterm", { timeout: 5000 }).catch(() => {
    // xterm may not mount if the container has zero size in headless — this is a known
    // xterm behaviour. We note this as a possible environment limitation.
  });
});

test("terminal container is present in agent view", async ({ page }) => {
  // The Terminal component mounts in the primary pane in agent view
  const terminalHost = page.locator('[data-pane="primary"] .terminal:not(.xterm)');
  await expect(terminalHost).toBeVisible({ timeout: 5000 });

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "terminal-mounted.png"), fullPage: true });
});

test("pty:data event causes xterm to write content", async ({ page }) => {
  // Emit pty data — the channel is "pty:data:" + paneId
  // Payload is number[] (byte array per wails.ts)
  const textBytes = Array.from(new TextEncoder().encode(TEST_TEXT));

  await page.evaluate(
    ({ channel, bytes }) => {
      (window as any).__emit(channel, bytes);
    },
    { channel: `pty:data:${PANE_ID}`, bytes: textBytes }
  );

  await page.waitForTimeout(800);

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "terminal-with-data.png"), fullPage: true });

  // xterm DOM renderer (no WebGL/Canvas addon) writes text into .xterm-rows spans.
  // The pane has a real height in Playwright's 1280×720 viewport, so xterm renders rows.
  await expect(page.locator('[data-pane="primary"] .xterm-rows')).toContainText(TEST_TEXT);
});

test("pty:exit event writes exit message", async ({ page }) => {
  // Emit pty:exit:<paneId> with { code: 0 }
  await page.evaluate(
    ({ channel }) => {
      (window as any).__emit(channel, { code: 0 });
    },
    { channel: `pty:exit:${PANE_ID}` }
  );

  await page.waitForTimeout(800);

  // Terminal.svelte writes `[process exited: ${code}]` into xterm on the exit event.
  // Assert the literal substring appears in .xterm-rows (ANSI codes are stripped by xterm's DOM renderer).
  await expect(page.locator('[data-pane="primary"] .xterm-rows')).toContainText("process exited", { timeout: 5000 });

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "terminal-after-exit.png"), fullPage: true });
});
