// frontend/src/lib/ApprovalFlow.test.ts
// Characterisation/lock tests for ApprovalCard + notification-store tiering.
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { vi, describe, it, expect, beforeEach } from "vitest";
import type { ApprovalReq, AgentCaps } from "./wails";

// ---------------------------------------------------------------------------
// ApprovalCard tests
// ---------------------------------------------------------------------------
// ApprovalCard imports only `type { ApprovalReq, AgentCaps }` from "./wails" —
// no runtime wails values are used by the component itself, so no mock is needed.

describe("ApprovalCard", () => {
  const req: ApprovalReq = { reqId: "req-1", tool: "Write", summary: "Write /tmp/foo.txt" };
  const caps: AgentCaps = { approvals: true, attention: false };
  const queue: ApprovalReq[] = [req];

  it("renders the tool name and summary", async () => {
    const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
    render(ApprovalCard, { props: { req, queue, caps, onDecision: vi.fn() } });
    await waitFor(() => {
      expect(screen.getByText("Write")).toBeInTheDocument();
      expect(screen.getByText("Write /tmp/foo.txt")).toBeInTheDocument();
    });
  });

  it("clicking Allow calls onDecision(reqId, 'allow')", async () => {
    const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
    const onDecision = vi.fn();
    render(ApprovalCard, { props: { req, queue, caps, onDecision } });
    await waitFor(() => screen.getByRole("button", { name: "Allow" }));
    await fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    expect(onDecision).toHaveBeenCalledWith("req-1", "allow");
  });

  it("clicking Deny calls onDecision(reqId, 'deny')", async () => {
    const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
    const onDecision = vi.fn();
    render(ApprovalCard, { props: { req, queue, caps, onDecision } });
    await waitFor(() => screen.getByRole("button", { name: "Deny" }));
    await fireEvent.click(screen.getByRole("button", { name: "Deny" }));
    expect(onDecision).toHaveBeenCalledWith("req-1", "deny");
  });

  it("clicking Always calls onDecision(reqId, 'always')", async () => {
    const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
    const onDecision = vi.fn();
    render(ApprovalCard, { props: { req, queue, caps, onDecision } });
    await waitFor(() => screen.getByRole("button", { name: "Always" }));
    await fireEvent.click(screen.getByRole("button", { name: "Always" }));
    expect(onDecision).toHaveBeenCalledWith("req-1", "always");
  });

  it("renders no buttons when caps.approvals is false", async () => {
    const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
    const noCaps: AgentCaps = { approvals: false, attention: false };
    render(ApprovalCard, { props: { req, queue, caps: noCaps, onDecision: vi.fn() } });
    expect(screen.queryByRole("button", { name: "Allow" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Deny" })).toBeNull();
  });

  it("shows batch buttons when queue has multiple items", async () => {
    const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
    const req2: ApprovalReq = { reqId: "req-2", tool: "Bash", summary: "Run build script" };
    const multiQueue = [req, req2];
    render(ApprovalCard, { props: { req, queue: multiQueue, caps, onDecision: vi.fn() } });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Approve all" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Deny all" })).toBeInTheDocument();
    });
  });

  it("Approve all calls onDecision for every item in queue", async () => {
    const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
    const req2: ApprovalReq = { reqId: "req-2", tool: "Bash", summary: "Run build script" };
    const multiQueue = [req, req2];
    const onDecision = vi.fn();
    render(ApprovalCard, { props: { req, queue: multiQueue, caps, onDecision } });
    await waitFor(() => screen.getByRole("button", { name: "Approve all" }));
    await fireEvent.click(screen.getByRole("button", { name: "Approve all" }));
    expect(onDecision).toHaveBeenCalledWith("req-1", "allow");
    expect(onDecision).toHaveBeenCalledWith("req-2", "allow");
    expect(onDecision).toHaveBeenCalledTimes(2);
  });
});

// ---------------------------------------------------------------------------
// Notification store tiering tests
// ---------------------------------------------------------------------------
// vi.resetModules() gives each test a fresh module with clean $state (items=[]).

describe("notification store tiering", () => {
  beforeEach(() => { vi.resetModules(); });

  it("addBlocking adds an item with tier 'blocking' and correct workspaceId", async () => {
    const { addBlocking, getItems } = await import("./stores/notifications.svelte");
    addBlocking("ws-42", "Needs approval", "Claude wants to write a file");
    const items = getItems();
    expect(items).toHaveLength(1);
    expect(items[0].tier).toBe("blocking");
    expect(items[0].workspaceId).toBe("ws-42");
    expect(items[0].title).toBe("Needs approval");
    expect(items[0].body).toBe("Claude wants to write a file");
    expect(items[0].read).toBe(false);
  });

  it("addAmbient adds an item with tier 'ambient'", async () => {
    const { addAmbient, getItems } = await import("./stores/notifications.svelte");
    addAmbient("ws-1", "Agent done", "Task finished");
    const items = getItems();
    expect(items[0].tier).toBe("ambient");
  });

  it("addRoutine adds an item with tier 'routine'", async () => {
    const { addRoutine, getItems } = await import("./stores/notifications.svelte");
    addRoutine("ws-1", "File changed", "/src/main.ts updated");
    const items = getItems();
    expect(items[0].tier).toBe("routine");
  });

  it("blocking items are not suppressed by DND", async () => {
    const { addBlocking, setDnd, getItems } = await import("./stores/notifications.svelte");
    setDnd(true);
    addBlocking("ws-1", "Urgent", "Requires action");
    expect(getItems()).toHaveLength(1);
  });

  it("ambient and routine items are silenced (logged as read) by DND, not dropped", async () => {
    const { addAmbient, addRoutine, setDnd, getItems } =
      await import("./stores/notifications.svelte");
    setDnd(true);
    addAmbient("ws-1", "Quiet", "background info");
    addRoutine("ws-1", "Bg", "routine task");
    // DND silences the interruption but keeps the record for away catch-up:
    // both are still in the hub, just recorded already-read (no unread badge).
    const items = getItems();
    expect(items).toHaveLength(2);
    expect(items.every((n) => n.read)).toBe(true);
  });
});
