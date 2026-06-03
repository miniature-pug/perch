/**
 * (c) menubar.spec.ts
 *
 * Keyboard navigation:
 * - open menu via Enter/click on trigger
 * - Arrow nav between items
 * - Escape closes AND focus returns to trigger
 * Screenshot: open menu state
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

test("click opens menu dropdown", async ({ page }) => {
  const sessionBtn = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: "Session" });
  await sessionBtn.click();
  await page.waitForTimeout(200);

  // Dropdown should be visible
  const dropdown = page.locator('[role="menu"]').first();
  await expect(dropdown).toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "menubar-open.png"), fullPage: true });
});

test("Enter key on trigger opens menu and focuses first item", async ({ page }) => {
  const sessionBtn = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: "Session" });
  await sessionBtn.focus();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(200);

  // Menu should be visible
  await expect(page.locator('[role="menu"]').first()).toBeVisible();

  // First menu item should be focused (requestAnimationFrame delay)
  await page.waitForTimeout(100);
  const focusedId = await page.evaluate(() => document.activeElement?.textContent?.trim());
  expect(focusedId, "First menu item should be focused after Enter").toBe("New session");
});

test("ArrowDown navigates through menu items", async ({ page }) => {
  const sessionBtn = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: "Session" });
  await sessionBtn.focus();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(200);

  // First item should have focus
  const firstFocused = await page.evaluate(() => document.activeElement?.textContent?.trim());
  expect(firstFocused).toBe("New session");

  // Arrow down moves to second item
  await page.keyboard.press("ArrowDown");
  const secondFocused = await page.evaluate(() => document.activeElement?.textContent?.trim());
  expect(secondFocused, "ArrowDown should move focus to 'Close session'").toBe("Close session");

  // Arrow down again moves to third item
  await page.keyboard.press("ArrowDown");
  const thirdFocused = await page.evaluate(() => document.activeElement?.textContent?.trim());
  expect(thirdFocused, "ArrowDown should move focus to 'Remove session'").toBe("Remove session");
});

test("ArrowUp navigates back through menu items", async ({ page }) => {
  const sessionBtn = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: "Session" });
  await sessionBtn.focus();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(200);

  // Navigate down twice then up
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowUp");

  const focused = await page.evaluate(() => document.activeElement?.textContent?.trim());
  expect(focused, "ArrowUp should move focus back to 'Close session'").toBe("Close session");
});

test("Escape closes menu and returns focus to trigger button", async ({ page }) => {
  const sessionBtn = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: "Session" });
  await sessionBtn.focus();
  await page.keyboard.press("Enter");
  await page.waitForTimeout(200);

  // Verify menu is open
  await expect(page.locator('[role="menu"]').first()).toBeVisible();

  // Press Escape
  await page.keyboard.press("Escape");
  await page.waitForTimeout(200);

  // Menu should be gone
  await expect(page.locator('[role="menu"]')).not.toBeVisible();

  // Focus must have returned to the trigger button
  const activeTag = await page.evaluate(() => document.activeElement?.textContent?.trim());
  expect(
    activeTag,
    "Focus should return to 'Session' trigger after Escape"
  ).toBe("Session");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "menubar-closed.png"), fullPage: true });
});

test("clicking outside closes menu", async ({ page }) => {
  const sessionBtn = page.locator('header[role="menubar"] button[role="menuitem"]', { hasText: "Session" });
  await sessionBtn.click();
  await page.waitForTimeout(200);

  await expect(page.locator('[role="menu"]').first()).toBeVisible();

  // Click on an inert area
  await page.click("body", { position: { x: 5, y: 400 } });
  await page.waitForTimeout(200);

  await expect(page.locator('[role="menu"]')).not.toBeVisible();
});
