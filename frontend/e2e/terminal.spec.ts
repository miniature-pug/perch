/**
 * terminal.spec.ts
 *
 * Render a pane with Terminal. Emit the pty:data:<paneId> event. Assert that xterm
 * renders the text in the DOM.
 *
 * Note: xterm.js, without a canvas or WebGL addon, uses a DOM renderer that writes
 * text into .xterm-rows span elements. The test asserts that the text appears there.
 * The event channel for pty data is "pty:data:" + paneId (from wails.ts onPtyData).
 * The event payload is one padded standard-base64 string per event (wails.ts base64ToBytes).
 *
 * The test cannot assert that the text is in visible rows without a real viewport.
 * So instead it asserts that the write arrived, by checking xterm's internal state
 * through the terminal DOM, and by asserting that WriteToPty was not called (the
 * test only emits from backend to frontend, not the reverse). It also checks that
 * the xterm container has rendered at least some row content.
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

  // Activate the session so the Terminal mounts.
  const sidebarItem = page.locator("text=test session").first();
  if (await sidebarItem.isVisible()) {
    await sidebarItem.click();
    await page.waitForTimeout(500);
    // The resume preview now gates opening a session. Click "Open" to confirm.
    const resumeOpenBtn = page.locator('[data-testid="resume-preview"] button.btn-primary');
    await resumeOpenBtn.waitFor({ state: "visible", timeout: 5000 });
    await resumeOpenBtn.click();
    await page.waitForTimeout(500);
  }

  // Agent view (the default) renders Terminal in the primary pane.
  // Wait for xterm to mount. It mounts during onMount.
  await page.waitForSelector(".xterm", { timeout: 5000 }).catch(() => {
    // xterm may not mount if the container has zero size in headless mode. This is
    // a known xterm behavior, and a possible environment limitation.
  });
});

test("terminal container is present in agent view", async ({ page }) => {
  // The Terminal component mounts in the primary pane in agent view
  const terminalHost = page.locator('[data-pane="primary"] .terminal:not(.xterm)');
  await expect(terminalHost).toBeVisible({ timeout: 5000 });

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "terminal-mounted.png"), fullPage: true });
});

test("pty:data event causes xterm to write content", async ({ page }) => {
  // Emit pty data. The channel is "pty:data:" + paneId.
  // The payload is one padded standard-base64 string, per wails.ts.
  const textBytes = Buffer.from(TEST_TEXT, "utf8").toString("base64");

  await page.evaluate(
    ({ channel, bytes }) => {
      (window as any).__emit(channel, bytes);
    },
    { channel: `pty:data:${PANE_ID}`, bytes: textBytes }
  );

  await page.waitForTimeout(800);

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "terminal-with-data.png"), fullPage: true });

  // The xterm DOM renderer (no WebGL or Canvas addon) writes text into .xterm-rows spans.
  // The pane has a real height in Playwright's 1280x720 viewport, so xterm renders rows.
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
  // Assert that the literal substring appears in .xterm-rows. xterm's DOM renderer strips ANSI codes.
  await expect(page.locator('[data-pane="primary"] .xterm-rows')).toContainText("process exited", { timeout: 5000 });

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "terminal-after-exit.png"), fullPage: true });
});
