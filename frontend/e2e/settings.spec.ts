/**
 * settings.spec.ts
 *
 * Open SettingsPanel; toggle theme (assert data-theme changes), density, font;
 * toggle DND; revoke an always-rule; assert SaveSettings called exactly once
 * with expected args. Screenshot panel.
 *
 * IPC method verified: SaveSettings (from wails.ts → app().SaveSettings(s: AppSettings))
 * Settings store calls: setTheme / setDensity / setFont / setDnd / setAlwaysRules
 * All of which ultimately call saveSettings → app().SaveSettings(snap)
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";

const INITIAL_SETTINGS = {
  theme: "gruvbox",
  density: "dense",
  font: "geist",
  dnd: false,
  alwaysRules: [
    { agent: "claude", tool: "bash", pattern: "npm run*" },
    { agent: "claude", tool: "write_file", pattern: "*.ts" },
  ],
};

async function openSettings(page: import("@playwright/test").Page) {
  // Click Session in menubar then Settings... via the menu
  // The easiest way: press ":" for palette then type "settings"
  await page.click(".app-root");
  await page.keyboard.press(":");
  await page.waitForTimeout(300);
  await page.keyboard.type("settings");
  await page.waitForTimeout(200);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(400);
}

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript({
    content: buildInitScriptContent({ settings: INITIAL_SETTINGS, workspaces: [] }),
  });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);
});

test("settings panel opens and displays", async ({ page }) => {
  await openSettings(page);

  const panel = page.locator('[role="dialog"][aria-label="Settings"]');
  await expect(panel).toBeVisible();

  // Theme select should show current theme
  await expect(page.locator('select[aria-label="Theme"]')).toHaveValue("gruvbox");
  await expect(page.locator('select[aria-label="Density"]')).toHaveValue("dense");
  await expect(page.locator('select[aria-label="Font"]')).toHaveValue("geist");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "settings-open.png"), fullPage: true });
});

test("changing theme updates data-theme attribute and calls SaveSettings", async ({ page }) => {
  await openSettings(page);

  // Capture SaveSettings call count before
  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );

  // Change theme to tokyo-night
  await page.locator('select[aria-label="Theme"]').selectOption("tokyo-night");
  await page.waitForTimeout(500);

  // data-theme on documentElement should be updated
  const dataTheme = await page.evaluate(() =>
    document.documentElement.getAttribute("data-theme")
  );
  expect(dataTheme, "data-theme should be updated to tokyo-night").toBe("tokyo-night");

  // SaveSettings should have been called once more
  const callsAfter = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );
  expect(
    callsAfter - callsBefore,
    "SaveSettings should be called exactly once after theme change"
  ).toBe(1);

  // Verify the arg has the new theme
  const lastSaveCall = await page.evaluate(() => {
    const calls = (window as any).__calls.filter((c: any) => c.method === "SaveSettings");
    return calls[calls.length - 1]?.args[0];
  });
  expect(lastSaveCall?.theme, "SaveSettings arg should have theme=tokyo-night").toBe("tokyo-night");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "settings-theme-changed.png"), fullPage: true });
});

test("changing density calls SaveSettings with new density", async ({ page }) => {
  await openSettings(page);

  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );

  await page.locator('select[aria-label="Density"]').selectOption("comfortable");
  await page.waitForTimeout(500);

  const callsAfter = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );
  expect(callsAfter - callsBefore).toBe(1);

  const lastArg = await page.evaluate(() => {
    const calls = (window as any).__calls.filter((c: any) => c.method === "SaveSettings");
    return calls[calls.length - 1]?.args[0];
  });
  expect(lastArg?.density).toBe("comfortable");
});

test("changing font calls SaveSettings with new font", async ({ page }) => {
  await openSettings(page);

  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );

  await page.locator('select[aria-label="Font"]').selectOption("inter");
  await page.waitForTimeout(500);

  const callsAfter = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );
  expect(callsAfter - callsBefore).toBe(1);

  const lastArg = await page.evaluate(() => {
    const calls = (window as any).__calls.filter((c: any) => c.method === "SaveSettings");
    return calls[calls.length - 1]?.args[0];
  });
  expect(lastArg?.font).toBe("inter");
});

test("toggling DND calls SaveSettings with dnd=true", async ({ page }) => {
  await openSettings(page);

  const dndSwitch = page.locator('button[role="switch"][aria-label="Do not disturb"]');
  await expect(dndSwitch).toHaveAttribute("aria-checked", "false");

  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );

  await dndSwitch.click();
  await page.waitForTimeout(500);

  await expect(dndSwitch).toHaveAttribute("aria-checked", "true");

  const callsAfter = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );
  expect(callsAfter - callsBefore).toBe(1);

  const lastArg = await page.evaluate(() => {
    const calls = (window as any).__calls.filter((c: any) => c.method === "SaveSettings");
    return calls[calls.length - 1]?.args[0];
  });
  expect(lastArg?.dnd).toBe(true);

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "settings-dnd-on.png"), fullPage: true });
});

test("revoking an always-rule calls SaveSettings with reduced list", async ({ page }) => {
  await openSettings(page);

  // Two rules should be listed
  const revokeButtons = page.locator('button[aria-label="Revoke"]');
  await expect(revokeButtons).toHaveCount(2);

  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );

  // Revoke the first rule
  await revokeButtons.first().click();
  await page.waitForTimeout(500);

  // Now only one rule should remain in the UI
  await expect(page.locator('button[aria-label="Revoke"]')).toHaveCount(1);

  // SaveSettings should be called once
  const callsAfter = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "SaveSettings").length
  );
  expect(callsAfter - callsBefore).toBe(1);

  // The arg should have one rule remaining
  const lastArg = await page.evaluate(() => {
    const calls = (window as any).__calls.filter((c: any) => c.method === "SaveSettings");
    return calls[calls.length - 1]?.args[0];
  });
  expect(lastArg?.alwaysRules?.length, "Should have 1 rule after revoking first").toBe(1);

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "settings-rule-revoked.png"), fullPage: true });
});

test("settings panel closes via Close button", async ({ page }) => {
  await openSettings(page);

  const panel = page.locator('[role="dialog"][aria-label="Settings"]');
  await expect(panel).toBeVisible();

  await page.locator('button[aria-label="close settings"]').click();
  await page.waitForTimeout(300);

  await expect(panel).not.toBeVisible();
});
