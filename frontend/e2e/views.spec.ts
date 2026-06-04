/**
 * (b) views.spec.ts
 *
 * With a mocked SELECTED workspace:
 * - agent / code / diff views via the segmented control; assert aria-pressed toggles
 * - Enter split mode via Split button; assert both data-pane panes present
 * - Screenshot each view + split
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent, WORKSPACE_FIXTURE } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript({
    content: buildInitScriptContent({
      workspaces: [WORKSPACE_FIXTURE],
    }),
  });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  // Select the workspace so views render (click sidebar item or simulate the select)
  // The workspace title is "test session" — click it to make it active
  const sidebarItem = page.locator("text=test session").first();
  if (await sidebarItem.isVisible()) {
    await sidebarItem.click();
    await page.waitForTimeout(500);
  }
});

test("agent view: Agent button is aria-pressed and primary pane renders", async ({ page }) => {
  // Agent view is default — verify aria-pressed="true" on Agent button
  const agentBtn = page.locator('nav[aria-label="View"] button', { hasText: "Agent" });
  await expect(agentBtn).toHaveAttribute("aria-pressed", "true");

  // Other buttons must NOT be pressed
  const codeBtn = page.locator('nav[aria-label="View"] button', { hasText: "Code" });
  const diffBtn = page.locator('nav[aria-label="View"] button', { hasText: "Diff" });
  await expect(codeBtn).toHaveAttribute("aria-pressed", "false");
  await expect(diffBtn).toHaveAttribute("aria-pressed", "false");

  // Primary pane present
  await expect(page.locator('[data-pane="primary"]')).toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "view-agent.png"), fullPage: true });
});

test("code view: clicking Code button toggles aria-pressed", async ({ page }) => {
  const codeBtn = page.locator('nav[aria-label="View"] button', { hasText: "Code" });
  await codeBtn.click();
  await page.waitForTimeout(300);

  await expect(codeBtn).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator('nav[aria-label="View"] button', { hasText: "Agent" })).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator('nav[aria-label="View"] button', { hasText: "Diff" })).toHaveAttribute("aria-pressed", "false");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "view-code.png"), fullPage: true });
});

test("diff view: clicking Diff button toggles aria-pressed", async ({ page }) => {
  const diffBtn = page.locator('nav[aria-label="View"] button', { hasText: "Diff" });
  await diffBtn.click();
  await page.waitForTimeout(300);

  await expect(diffBtn).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator('nav[aria-label="View"] button', { hasText: "Agent" })).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator('nav[aria-label="View"] button', { hasText: "Code" })).toHaveAttribute("aria-pressed", "false");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "view-diff.png"), fullPage: true });
});

test("split mode: Split button renders both primary and secondary panes", async ({ page }) => {
  // Click Split pane button
  const splitBtn = page.locator('button[aria-label="Split pane"]');
  await expect(splitBtn).toBeVisible();
  await splitBtn.click();
  await page.waitForTimeout(300);

  // Both panes must be present in the DOM
  await expect(page.locator('[data-pane="primary"]')).toBeVisible();
  await expect(page.locator('[data-pane="secondary"]')).toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "view-split.png"), fullPage: true });
});

test("split mode: splitting and un-splitting removes secondary pane", async ({ page }) => {
  const splitBtn = page.locator('button[aria-label="Split pane"]');
  await splitBtn.click();
  await page.waitForTimeout(300);
  await expect(page.locator('[data-pane="secondary"]')).toBeVisible();

  // Click again to toggle split off
  await splitBtn.click();
  await page.waitForTimeout(300);
  await expect(page.locator('[data-pane="secondary"]')).not.toBeVisible();
});

test("empty state renders when no workspace is selected", async ({ browser }) => {
  // Separate browser context to avoid stacking init scripts from beforeEach
  const context = await browser.newContext({ baseURL: "http://localhost:4173" });
  await context.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  const page = await context.newPage();
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  // First-run empty state: a "New Session" CTA + quick-start templates (SPEC §7.7).
  await expect(page.locator('[data-testid="empty-state"]')).toBeVisible();
  await expect(page.locator('.empty-state-btn-primary')).toHaveText("New Session");
  await expect(page.locator('text=Claude session')).toBeVisible();
  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "view-empty.png"), fullPage: true });
  await context.close();
});
