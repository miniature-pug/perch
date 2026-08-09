/**
 * approval.spec.ts
 *
 * Trigger an approval request via window.__emit("agent:event", payload):
 * - ApprovalCard renders when caps.approvals=true and an approval is in state
 * - Click Allow → Approve IPC called with (reqId, "allow")
 * - Click Deny → Approve IPC called with (reqId, "deny")
 * - Click Always → Approve IPC called with (reqId, "always")
 *
 * IPC method: Approve(reqId, decision) from wails.ts
 * Event channel: "agent:event" (onAgentEvent)
 * Payload: AgentEvent { workspaceId, kind: "approval", approval: ApprovalReq }
 */

import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { buildInitScriptContent, WORKSPACE_FIXTURE } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";

const APPROVAL_REQ = {
  reqId: "req-abc-123",
  tool: "bash",
  summary: "Run: npm install",
};

async function triggerApproval(page: import("@playwright/test").Page) {
  // Emit an agent:event with kind=approval and the workspace's id
  await page.evaluate(
    ({ wsId, req }) => {
      (window as any).__emit("agent:event", {
        workspaceId: wsId,
        kind: "approval",
        approval: req,
      });
    },
    { wsId: WORKSPACE_FIXTURE.id, req: APPROVAL_REQ }
  );
  await page.waitForTimeout(500);
}

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

  // Activate the workspace
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
});

test("ApprovalCard renders after agent:event with kind=approval", async ({ page }) => {
  await triggerApproval(page);

  const card = page.locator('section[aria-label="approval card"]');
  await expect(card).toBeVisible();

  // Tool name and summary should be displayed
  await expect(card.locator(".tool-name")).toContainText("bash");
  await expect(card.locator(".approval-summary")).toContainText("npm install");

  // All three action buttons present (exact "Allow" avoids matching "Always allow")
  await expect(card.getByRole("button", { name: "Allow", exact: true })).toBeVisible();
  await expect(card.locator("button", { hasText: "Deny" })).toBeVisible();
  await expect(card.getByRole("button", { name: "Always allow" })).toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "approval-card.png"), fullPage: true });
});

test("clicking Allow calls Approve with (reqId, 'allow')", async ({ page }) => {
  await triggerApproval(page);

  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "Approve").length
  );

  const card = page.locator('section[aria-label="approval card"]');
  await card.getByRole("button", { name: "Allow", exact: true }).click();
  await page.waitForTimeout(500);

  const approveCalls = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "Approve")
  );
  expect(approveCalls.length - callsBefore, "Approve should be called once").toBe(1);

  const lastCall = approveCalls[approveCalls.length - 1];
  expect(lastCall.args[0], "reqId should match").toBe(APPROVAL_REQ.reqId);
  expect(lastCall.args[1], "decision should be 'allow'").toBe("allow");

  // Card should be gone after approval
  await expect(card).not.toBeVisible();

  await page.screenshot({ path: path.join(SCREENSHOT_DIR, "approval-after-allow.png"), fullPage: true });
});

test("clicking Deny calls Approve with (reqId, 'deny')", async ({ page }) => {
  await triggerApproval(page);

  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "Approve").length
  );

  const card = page.locator('section[aria-label="approval card"]');
  await card.locator("button", { hasText: "Deny" }).click();
  await page.waitForTimeout(500);

  const approveCalls = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "Approve")
  );
  expect(approveCalls.length - callsBefore).toBe(1);

  const lastCall = approveCalls[approveCalls.length - 1];
  expect(lastCall.args[0]).toBe(APPROVAL_REQ.reqId);
  expect(lastCall.args[1]).toBe("deny");
});

test("clicking Always calls Approve with (reqId, 'always')", async ({ page }) => {
  await triggerApproval(page);

  const callsBefore = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "Approve").length
  );

  const card = page.locator('section[aria-label="approval card"]');
  await card.locator("button", { hasText: "Always" }).click();
  await page.waitForTimeout(500);

  const approveCalls = await page.evaluate(() =>
    (window as any).__calls.filter((c: any) => c.method === "Approve")
  );
  expect(approveCalls.length - callsBefore).toBe(1);

  const lastCall = approveCalls[approveCalls.length - 1];
  expect(lastCall.args[0]).toBe(APPROVAL_REQ.reqId);
  expect(lastCall.args[1]).toBe("always");
});
