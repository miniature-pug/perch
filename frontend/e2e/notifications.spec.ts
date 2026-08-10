/**
 * notifications.spec.ts
 *
 * Trigger the blocking, ambient, and routine notification tiers through the
 * "notify" event channel. Assert that each renders with its tier CSS class
 * and the correct structure.
 *
 * Event channel: "notify" (onNotify in wails.ts)
 * Payload: { tier: "blocking"|"ambient"|"routine"; title: string; body: string; workspaceId: string }
 *
 * The notification hub shows when notifOpen=true.
 * The bell button triggers runCommand("notifications:open"), which toggles notifOpen.
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";

async function openNotificationHub(page: import("@playwright/test").Page) {
  // Click the bell button in the menubar
  await page.locator('button[aria-label="notifications"]').click();
  await page.waitForTimeout(300);
  await expect(page.locator('section[aria-label="notification hub"]')).toBeVisible();
}

async function emitNotification(
  page: import("@playwright/test").Page,
  tier: "blocking" | "ambient" | "routine",
  title: string,
  body: string,
  workspaceId = "ws-test"
) {
  await page.evaluate(
    ({ tier, title, body, workspaceId }) => {
      (window as any).__emit("notify", { tier, title, body, workspaceId });
    },
    { tier, title, body, workspaceId }
  );
  await page.waitForTimeout(300);
}

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1000);
});

test("blocking notification renders with tier-blocking class", async ({ page }) => {
  await emitNotification(page, "blocking", "Approval Required", "bash is requesting access");
  await openNotificationHub(page);

  const item = page.locator(".notif-item.tier-blocking");
  await expect(item).toBeVisible();
  await expect(item.locator(".notif-title")).toContainText("Approval Required");
  await expect(item.locator(".notif-body")).toContainText("bash is requesting access");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "notification-blocking.png"), fullPage: true });
});

test("ambient notification renders with tier-ambient class", async ({ page }) => {
  await emitNotification(page, "ambient", "Task Complete", "Agent finished the task");
  await openNotificationHub(page);

  const item = page.locator(".notif-item.tier-ambient");
  await expect(item).toBeVisible();
  await expect(item.locator(".notif-title")).toContainText("Task Complete");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "notification-ambient.png"), fullPage: true });
});

test("routine notification renders with tier-routine class", async ({ page }) => {
  await emitNotification(page, "routine", "File saved", "main.go was written");
  await openNotificationHub(page);

  const item = page.locator(".notif-item.tier-routine");
  await expect(item).toBeVisible();
  await expect(item.locator(".notif-title")).toContainText("File saved");

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "notification-routine.png"), fullPage: true });
});

test("multiple tiers all render in the hub", async ({ page }) => {
  await emitNotification(page, "blocking", "Block: Approval Required", "approval needed");
  await emitNotification(page, "ambient", "Ambient: Done", "done");
  await emitNotification(page, "routine", "Routine: Log", "log entry");
  await openNotificationHub(page);

  await expect(page.locator(".notif-item.tier-blocking")).toBeVisible();
  await expect(page.locator(".notif-item.tier-ambient")).toBeVisible();
  await expect(page.locator(".notif-item.tier-routine")).toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "notification-all-tiers.png"), fullPage: true });
});

test("dismissing a notification marks it read (dismiss button present)", async ({ page }) => {
  await emitNotification(page, "ambient", "Dismiss Me", "body text");
  await openNotificationHub(page);

  const item = page.locator(".notif-item").first();
  await expect(item).toBeVisible();

  // Dismiss button exists
  const dismissBtn = item.locator('button[aria-label="dismiss notification"]');
  await expect(dismissBtn).toBeVisible();

  await dismissBtn.click();
  await page.waitForTimeout(300);

  // After dismiss, item gets .read class (markRead sets read=true)
  // The item stays in the list but gains .read
  const readItem = page.locator(".notif-item.read");
  await expect(readItem).toBeVisible();
});

test("DND silences ambient/routine (logged as read) but never blocking", async ({ page }) => {
  // Toggle DND on: click the bell, then the hub, then the "Do not disturb" button.
  await openNotificationHub(page);
  const dndBtn = page.locator('section[aria-label="notification hub"] button', {
    hasText: "Do not disturb",
  });
  await dndBtn.click();
  await page.waitForTimeout(300);
  // Close hub
  await page.locator('button[aria-label="notifications"]').click();
  await page.waitForTimeout(200);

  // Emit an ambient notification. Under DND, the app silences it (logs it as read) but does not drop it.
  await emitNotification(page, "ambient", "Silenced Ambient", "still logged, just read");
  // Emit a blocking notification. DND never silences blocking notifications.
  await emitNotification(page, "blocking", "Critical Block", "must show");

  // Open hub
  await openNotificationHub(page);

  // Blocking notification present (DND never silences tier 1)
  await expect(page.locator(".notif-item.tier-blocking")).toBeVisible();

  // The ambient notification is present. DND silenced it rather than dropping it, and marked
  // it read. It stays in the away catch-up log, and does not increase the unread badge count.
  await expect(page.locator(".notif-item.tier-ambient.read")).toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "notification-dnd-active.png"), fullPage: true });
});
