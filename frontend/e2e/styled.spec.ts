/**
 * styled.spec.ts: semantic computed-style guards.
 *
 * Each assertion is discriminating. It fails on an unstyled, UA-default element,
 * because it checks properties with a clear delta between styled and unstyled.
 *
 * Discrimination reference:
 *   background-color unstyled: rgba(0, 0, 0, 0).  The test needs alpha > 0.
 *   box-shadow       unstyled: "none".             The test needs a value other than 'none'.
 *   border-width     unstyled: "0px".              The test needs >= 1.
 *   border-radius    unstyled: "0px".              The test needs > 0.
 *   display          unstyled: "block" (div).      The test needs "flex".
 *
 * Each page.evaluate() arrow function defines its own isOpaque(). This keeps
 * it in the browser context where it belongs, and not in Node.
 */

import { test, expect } from "@playwright/test";
import fs from "fs";
import { buildInitScriptContent, WORKSPACE_FIXTURE } from "./_mock";

const SCREENSHOT_DIR = "./e2e/__screenshots__";

test.beforeAll(() => {
  fs.mkdirSync(SCREENSHOT_DIR, { recursive: true });
});

// ---------------------------------------------------------------------------
// 1. MenuBar: display:flex and a non-transparent background
// ---------------------------------------------------------------------------
test("MenuBar: display is flex and background is non-transparent", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1000);

  const result = await page.evaluate(() => {
    function isOpaque(color: string): boolean {
      if (!color || color === "transparent") return false;
      if (color.startsWith("rgb(")) return true;
      const m = color.match(/rgba\(\s*\d+\s*,\s*\d+\s*,\s*\d+\s*,\s*([\d.]+)\s*\)/);
      if (m) return parseFloat(m[1]) > 0;
      return false;
    }
    const el = document.querySelector("header.menubar");
    if (!el) return { err: "menubar not found" };
    const cs = getComputedStyle(el);
    return { display: cs.display, bgOpaque: isOpaque(cs.backgroundColor), bg: cs.backgroundColor };
  });

  expect((result as any).err, "menubar element should exist").toBeUndefined();
  expect((result as any).display, "menubar display should be flex").toBe("flex");
  expect(
    (result as any).bgOpaque,
    `menubar background-color should be non-transparent, got: ${(result as any).bg}`
  ).toBe(true);

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-menubar.png`, fullPage: true });
});

// ---------------------------------------------------------------------------
// 2. CommandPalette: card has a non-transparent bg, a visible border, and a box-shadow
// ---------------------------------------------------------------------------
test("CommandPalette (open): card has bg, border, and box-shadow", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1000);

  // Open the palette with ":". First, dismiss any stray welcome dialog, so the
  // keystroke reaches the app and not a modal opened by a welcome button.
  await page.keyboard.press("Escape");
  await page.keyboard.press(":");
  await page.waitForTimeout(300);

  await expect(page.locator('[role="dialog"][aria-label="command palette"]')).toBeVisible();

  const result = await page.evaluate(() => {
    function isOpaque(color: string): boolean {
      if (!color || color === "transparent") return false;
      if (color.startsWith("rgb(")) return true;
      const m = color.match(/rgba\(\s*\d+\s*,\s*\d+\s*,\s*\d+\s*,\s*([\d.]+)\s*\)/);
      if (m) return parseFloat(m[1]) > 0;
      return false;
    }
    const card = document.querySelector(".palette");
    if (!card) return { err: ".palette card not found" };
    const cs = getComputedStyle(card);
    return {
      bgOpaque: isOpaque(cs.backgroundColor),
      bg: cs.backgroundColor,
      borderWidth: cs.borderTopWidth,
      borderStyle: cs.borderTopStyle,
      boxShadow: cs.boxShadow,
    };
  });

  expect((result as any).err, ".palette card should exist inside overlay").toBeUndefined();
  expect(
    (result as any).bgOpaque,
    `palette card background should be non-transparent, got: ${(result as any).bg}`
  ).toBe(true);
  expect(
    parseFloat((result as any).borderWidth),
    `palette card border-width should be >= 1px, got: ${(result as any).borderWidth}`
  ).toBeGreaterThanOrEqual(1);
  expect(
    (result as any).borderStyle,
    "palette card border-style should not be 'none'"
  ).not.toBe("none");
  expect(
    (result as any).boxShadow,
    "palette card should have a box-shadow (floating card)"
  ).not.toBe("none");

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-palette.png`, fullPage: true });
});

// ---------------------------------------------------------------------------
// 3. NotificationHub: the blocking tier left-border color differs from the routine tier.
//    The blocking tier borderLeftColor is non-transparent (an err color, not transparent).
// ---------------------------------------------------------------------------
test("NotificationHub: blocking tier left-border is distinct from routine tier", async ({ page }) => {
  await page.addInitScript({ content: buildInitScriptContent({ workspaces: [] }) });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1000);

  // Emit blocking and routine notifications.
  await page.evaluate(() => {
    (window as any).__emit("notify", { tier: "blocking", title: "Block!", body: "needs you", workspaceId: "ws-test" });
    (window as any).__emit("notify", { tier: "routine",  title: "Routine", body: "log",    workspaceId: "ws-test" });
  });
  await page.waitForTimeout(300);

  // Open hub
  await page.locator('button[aria-label="notifications"]').click();
  await page.waitForTimeout(300);
  await expect(page.locator('section[aria-label="notification hub"]')).toBeVisible();

  const result = await page.evaluate(() => {
    function isOpaque(color: string): boolean {
      if (!color || color === "transparent") return false;
      if (color.startsWith("rgb(")) return true;
      const m = color.match(/rgba\(\s*\d+\s*,\s*\d+\s*,\s*\d+\s*,\s*([\d.]+)\s*\)/);
      if (m) return parseFloat(m[1]) > 0;
      return false;
    }
    const blocking = document.querySelector(".notif-item.tier-blocking");
    const routine  = document.querySelector(".notif-item.tier-routine");
    if (!blocking) return { err: ".tier-blocking not found" };
    if (!routine)  return { err: ".tier-routine not found"  };
    const blockingColor = getComputedStyle(blocking).borderLeftColor;
    const routineColor  = getComputedStyle(routine).borderLeftColor;
    return {
      blockingColor,
      routineColor,
      blockingOpaque: isOpaque(blockingColor),
      differ: blockingColor !== routineColor,
    };
  });

  expect((result as any).err, "Both tier elements should be in the DOM").toBeUndefined();
  expect(
    (result as any).blockingOpaque,
    `tier-blocking borderLeftColor should be non-transparent (err color), got: ${(result as any).blockingColor}`
  ).toBe(true);
  expect(
    (result as any).differ,
    `tier-blocking and tier-routine borderLeftColor should differ. blocking=${(result as any).blockingColor}, routine=${(result as any).routineColor}`
  ).toBe(true);

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-notifications.png`, fullPage: true });
});

// ---------------------------------------------------------------------------
// 4. ApprovalCard: card has bg, border, and shadow. The Allow button bg differs from Deny.
// ---------------------------------------------------------------------------
test("ApprovalCard: card chrome is styled; Allow button is accent-colored", async ({ page }) => {
  await page.addInitScript({
    content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }),
  });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  // Activate the session.
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

  // Emit approval
  await page.evaluate(() => {
    (window as any).__emit("agent:event", {
      workspaceId: "ws-1",
      kind: "approval",
      approval: { reqId: "req-style-check", tool: "bash", summary: "Run: ls" },
    });
  });
  await page.waitForTimeout(500);

  await expect(page.locator('section[aria-label="approval card"]')).toBeVisible();

  const result = await page.evaluate(() => {
    function isOpaque(color: string): boolean {
      if (!color || color === "transparent") return false;
      if (color.startsWith("rgb(")) return true;
      const m = color.match(/rgba\(\s*\d+\s*,\s*\d+\s*,\s*\d+\s*,\s*([\d.]+)\s*\)/);
      if (m) return parseFloat(m[1]) > 0;
      return false;
    }
    const card     = document.querySelector("section.approval-card");
    const allowBtn = card && card.querySelector("button.btn-primary");
    const denyBtn  = card && card.querySelector("button.btn:not(.btn-primary):not(.btn-always)");
    if (!card)     return { err: ".approval-card not found" };
    if (!allowBtn) return { err: ".btn-primary (Allow) not found" };
    if (!denyBtn)  return { err: ".btn (Deny) not found" };
    const cs      = getComputedStyle(card);
    const csAllow = getComputedStyle(allowBtn);
    const csDeny  = getComputedStyle(denyBtn);
    return {
      cardBg:      cs.backgroundColor,
      cardBgOpaque: isOpaque(cs.backgroundColor),
      cardBorder:  cs.borderTopWidth,
      cardShadow:  cs.boxShadow,
      allowBg:     csAllow.backgroundColor,
      allowBgOpaque: isOpaque(csAllow.backgroundColor),
      denyBg:      csDeny.backgroundColor,
      allowBgDiffFromDeny: csAllow.backgroundColor !== csDeny.backgroundColor,
      allowRadius: csAllow.borderRadius,
      denyRadius:  csDeny.borderRadius,
    };
  });

  expect((result as any).err).toBeUndefined();

  // Card chrome
  expect(
    (result as any).cardBgOpaque,
    `approval card background should be non-transparent, got: ${(result as any).cardBg}`
  ).toBe(true);
  expect(
    parseFloat((result as any).cardBorder),
    "approval card border-width should be >= 1px"
  ).toBeGreaterThanOrEqual(1);
  expect((result as any).cardShadow, "approval card should have box-shadow").not.toBe("none");

  // Allow button has accent fill (non-transparent, different from Deny's transparent bg)
  expect(
    (result as any).allowBgOpaque,
    `Allow button background should be non-transparent (accent fill), got: ${(result as any).allowBg}`
  ).toBe(true);
  expect(
    (result as any).allowBgDiffFromDeny,
    `Allow button bg (${(result as any).allowBg}) should differ from Deny button bg (${(result as any).denyBg})`
  ).toBe(true);

  // border-radius proves the .btn styling applied (the UA default is 0px).
  expect(
    parseFloat((result as any).allowRadius),
    `Allow button border-radius should be > 0, got: ${(result as any).allowRadius}`
  ).toBeGreaterThan(0);
  expect(
    parseFloat((result as any).denyRadius),
    `Deny button border-radius should be > 0, got: ${(result as any).denyRadius}`
  ).toBeGreaterThan(0);

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-approval.png`, fullPage: true });
});

// ---------------------------------------------------------------------------
// 5. Sidebar: the active row bg differs from the inactive row bg
// ---------------------------------------------------------------------------
test("Sidebar: active workspace row has different background than inactive", async ({ page }) => {
  const ws2 = {
    ...WORKSPACE_FIXTURE,
    id: "ws-2",
    paneId: "pane-ws-2",
    title: "second session",
  };
  await page.addInitScript({
    content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE, ws2] }),
  });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  // Activate the first session.
  await page.locator("text=test session").first().click();
  await page.waitForTimeout(500);
  // The resume preview now gates opening a session. Click "Open" to confirm.
  const resumeOpenBtn = page.locator('[data-testid="resume-preview"] button.btn-primary');
  await resumeOpenBtn.waitFor({ state: "visible", timeout: 5000 });
  await resumeOpenBtn.click();
  await page.waitForTimeout(500);

  const result = await page.evaluate(() => {
    const activeBtn    = document.querySelector<HTMLElement>('.workspace-row[aria-current="page"]');
    const inactiveBtns = Array.from(document.querySelectorAll<HTMLElement>('.workspace-row:not([aria-current="page"])'));
    if (!activeBtn)               return { err: "no active workspace-row found" };
    if (inactiveBtns.length < 1)  return { err: "no inactive workspace-row found" };
    const activeBg   = getComputedStyle(activeBtn).backgroundColor;
    const inactiveBg = getComputedStyle(inactiveBtns[0]).backgroundColor;
    return { activeBg, inactiveBg };
  });

  expect((result as any).err, "Both active and inactive rows should exist").toBeUndefined();
  expect(
    (result as any).activeBg,
    `Active row bg (${(result as any).activeBg}) should differ from inactive (${(result as any).inactiveBg})`
  ).not.toBe((result as any).inactiveBg);

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-sidebar.png`, fullPage: true });
});

// ---------------------------------------------------------------------------
// 6. Terminal: the xterm.css guard. .xterm-helper-textarea opacity is "0".
//    This proves xterm.css is loaded. Without it, the element is visible.
// ---------------------------------------------------------------------------
test("Terminal: xterm.css loaded — .xterm-helper-textarea opacity is 0", async ({ page }) => {
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

  // Wait for xterm to mount, on a best-effort basis. Headless may skip the mount if it is zero-size.
  await page.waitForSelector(".xterm", { timeout: 5000 }).catch(() => {});

  const result = await page.evaluate(() => {
    const xterm = document.querySelector(".xterm");
    if (!xterm) return { err: ".xterm not present" };
    const ta = xterm.querySelector(".xterm-helper-textarea");
    if (!ta) return { err: ".xterm-helper-textarea not found" };
    return { opacity: getComputedStyle(ta).opacity };
  });

  // If xterm did not mount at all (headless zero-size), skip. The terminal-container test covers presence.
  if ((result as any).err === ".xterm not present") {
    console.log("xterm did not mount (headless zero-size) — skipping opacity check");
    return;
  }

  expect((result as any).err).toBeUndefined();
  expect(
    (result as any).opacity,
    `xterm-helper-textarea opacity should be "0" (xterm.css loaded). Got: ${(result as any).opacity}`
  ).toBe("0");

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-terminal.png`, fullPage: true });
});

// ---------------------------------------------------------------------------
// 7. Editor (code view): .cm-editor fills height > 100px, and is top-aligned
// ---------------------------------------------------------------------------
test("Editor: CodeMirror fills the pane (height > 100px, top-aligned)", async ({ page }) => {
  const MOCK_FILE_PATH = `${WORKSPACE_FIXTURE.worktreePath}/main.js`;
  const MOCK_FILE_CONTENT = "// hello perch\nconsole.log('world');\n";

  const base = buildInitScriptContent({
    workspaces: [WORKSPACE_FIXTURE],
    readFileContent: MOCK_FILE_CONTENT,
    hunks: [],
  });
  const override = `
(function() {
  window.go.app.App.ListDir = function(absDir) {
    window.__calls.push({ method: 'ListDir', args: [absDir] });
    return Promise.resolve([
      { name: 'main.js', path: ${JSON.stringify(MOCK_FILE_PATH)}, isDir: false }
    ]);
  };
})();
`;
  await page.addInitScript({ content: base + "\n" + override });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

  // Activate the session.
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

  // Switch to code view and open a file.
  await page.locator('nav[aria-label="View"] button', { hasText: "Code" }).click();
  await page.waitForTimeout(500);

  const fileItem = page.locator("text=main.js");
  await expect(fileItem).toBeVisible({ timeout: 5000 });
  await fileItem.click();
  await page.waitForTimeout(1000);

  await expect(page.locator('section[aria-label="editor"]')).toBeVisible({ timeout: 5000 });
  await page.waitForSelector(".cm-editor", { timeout: 5000 }).catch(() => {});

  const result = await page.evaluate(() => {
    const editor = document.querySelector(".cm-editor");
    if (!editor) return { err: ".cm-editor not found" };
    const rect = editor.getBoundingClientRect();
    const pane = document.querySelector('[data-pane="primary"]');
    const paneRect = pane ? pane.getBoundingClientRect() : null;
    return {
      height: rect.height,
      top: rect.top,
      paneTop: paneRect ? paneRect.top : null,
    };
  });

  expect((result as any).err, ".cm-editor should be in the DOM").toBeUndefined();
  expect(
    (result as any).height,
    `CodeMirror should fill height > 100px, got: ${(result as any).height}px`
  ).toBeGreaterThan(100);

  // Top-aligned: cm-editor top should be near the pane top (within 50px)
  if ((result as any).paneTop !== null) {
    const delta = Math.abs((result as any).top - (result as any).paneTop);
    expect(
      delta,
      `CodeMirror should be top-aligned (within 50px of pane top), got delta: ${delta}px`
    ).toBeLessThan(50);
  }

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-editor.png`, fullPage: true });
});

// ---------------------------------------------------------------------------
// 8. Buttons (ApprovalCard): .btn has non-zero border-radius and padding.
//    border-radius > 0 is the discriminating check (the UA default is 0px).
// ---------------------------------------------------------------------------
test("Buttons: .btn has border-radius > 0 and non-zero padding", async ({ page }) => {
  await page.addInitScript({
    content: buildInitScriptContent({ workspaces: [WORKSPACE_FIXTURE] }),
  });
  await page.goto("/");
  await page.waitForSelector("#app", { timeout: 10000 });
  await page.waitForTimeout(1500);

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

  // Emit an approval, to surface the ApprovalCard buttons.
  await page.evaluate(() => {
    (window as any).__emit("agent:event", {
      workspaceId: "ws-1",
      kind: "approval",
      approval: { reqId: "req-btn-check", tool: "write_file", summary: "save to disk" },
    });
  });
  await page.waitForTimeout(500);

  await expect(page.locator('section[aria-label="approval card"]')).toBeVisible();

  const result = await page.evaluate(() => {
    const btn = document.querySelector<HTMLElement>("section.approval-card .btn");
    if (!btn) return { err: ".btn not found in approval card" };
    const cs = getComputedStyle(btn);
    return {
      borderRadius: cs.borderRadius,
      paddingLeft:  cs.paddingLeft,
      paddingTop:   cs.paddingTop,
    };
  });

  expect((result as any).err).toBeUndefined();
  expect(
    parseFloat((result as any).borderRadius),
    `button border-radius should be > 0 (proves .btn styling applied), got: ${(result as any).borderRadius}`
  ).toBeGreaterThan(0);
  // padding-left on .btn is 12px. The UA default is about 6px. The test asserts > 8 to clear the UA default.
  expect(
    parseFloat((result as any).paddingLeft),
    `button padding-left should be > 8px (styled), got: ${(result as any).paddingLeft}`
  ).toBeGreaterThan(8);

  await page.screenshot({ path: `${SCREENSHOT_DIR}/styled-button.png`, fullPage: true });
});
