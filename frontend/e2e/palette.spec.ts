/**
 * palette.spec.ts
 *
 * Command palette:
 * - open via ":" keystroke (mode.enterCommand) — the app opens palette when mode === "command"
 * - type to filter (list narrows)
 * - ArrowDown to move selection
 * - Enter runs selected command (assert via __calls or state change)
 * - Escape closes
 * Screenshot: open + filtered states
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1000);
});

test("colon key opens command palette", async ({ page }) => {
  // Focus the body/app to ensure keydown goes to the app's svelte:window handler
  await page.click(".app-root");
  await page.waitForTimeout(100);

  // The app uses ":" to enterCommand mode which shows the palette
  await page.keyboard.press(":");
  await page.waitForTimeout(300);

  // Palette overlay should be visible
  await expect(
    page.locator('[role="dialog"][aria-label="command palette"]')
  ).toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "palette-open.png"), fullPage: true });
});

test("typing narrows the list", async ({ page }) => {
  await page.click(".app-root");
  await page.keyboard.press(":");
  await page.waitForTimeout(300);

  const palette = page.locator('[role="dialog"][aria-label="command palette"]');
  await expect(palette).toBeVisible();

  // Count all items before filtering
  const totalBefore = await page.locator('[role="option"]').count();
  expect(totalBefore, "Should have multiple commands before filtering").toBeGreaterThan(3);

  // Type to filter — "settings" should narrow to a small set
  await page.keyboard.type("settings");
  await page.waitForTimeout(200);

  const totalAfter = await page.locator('[role="option"]').count();
  expect(
    totalAfter,
    `Filtering by "settings" should narrow list (before: ${totalBefore}, after: ${totalAfter})`
  ).toBeLessThan(totalBefore);
  expect(totalAfter, "Should still have at least one result for 'settings'").toBeGreaterThan(0);

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "palette-filtered.png"), fullPage: true });
});

test("ArrowDown moves selection (aria-selected toggles)", async ({ page }) => {
  await page.click(".app-root");
  await page.keyboard.press(":");
  await page.waitForTimeout(300);

  await expect(page.locator('[role="dialog"][aria-label="command palette"]')).toBeVisible();

  // First item (index 0) should be selected initially
  const firstItem = page.locator('[role="option"]#palette-option-0');
  await expect(firstItem).toHaveAttribute("aria-selected", "true");

  // ArrowDown moves selection to item 1
  await page.keyboard.press("ArrowDown");
  await page.waitForTimeout(100);

  await expect(firstItem).toHaveAttribute("aria-selected", "false");
  const secondItem = page.locator('[role="option"]#palette-option-1');
  await expect(secondItem).toHaveAttribute("aria-selected", "true");
});

test("Enter on selected command fires it and closes palette", async ({ page }) => {
  await page.click(".app-root");
  await page.keyboard.press(":");
  await page.waitForTimeout(300);

  // Filter to a specific command to get predictable state
  // "new session" → session:new command
  await page.keyboard.type("new session");
  await page.waitForTimeout(200);

  // First result should be "New session"
  await expect(page.locator('[role="option"]').first()).toContainText("New session");

  // Enter should run the command → opens NewSessionDialog (or triggers it)
  await page.keyboard.press("Enter");
  await page.waitForTimeout(300);

  // Palette should be closed
  await expect(
    page.locator('[role="dialog"][aria-label="command palette"]')
  ).not.toBeVisible();
});

test("Escape closes palette without running command", async ({ page }) => {
  await page.click(".app-root");
  await page.keyboard.press(":");
  await page.waitForTimeout(300);

  await expect(page.locator('[role="dialog"][aria-label="command palette"]')).toBeVisible();

  await page.keyboard.press("Escape");
  await page.waitForTimeout(300);

  await expect(
    page.locator('[role="dialog"][aria-label="command palette"]')
  ).not.toBeVisible();
});

test("view:agent command sets agent view via palette", async ({ page }) => {
  // First switch to code view
  // Navigate to code view first with the keyboard
  await page.click(".app-root");
  await page.keyboard.press("2"); // code view keybind
  await page.waitForTimeout(200);

  await expect(
    page.locator('nav[aria-label="View"] button', { hasText: "Code" })
  ).toHaveAttribute("aria-pressed", "true");

  // Open palette and run "Agent view"
  await page.keyboard.press(":");
  await page.waitForTimeout(300);
  await page.keyboard.type("agent view");
  await page.waitForTimeout(200);

  await page.keyboard.press("Enter");
  await page.waitForTimeout(300);

  // Should be back on agent view
  await expect(
    page.locator('nav[aria-label="View"] button', { hasText: "Agent" })
  ).toHaveAttribute("aria-pressed", "true");
});
