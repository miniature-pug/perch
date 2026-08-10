// frontend/src/lib/ApprovalCard.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const singleReq  = { reqId: "req_1", tool: "Bash",     summary: "run: ls -la /tmp" };
const batchQueue = [
  { reqId: "req_1", tool: "Bash",      summary: "run: ls -la /tmp" },
  { reqId: "req_2", tool: "WriteFile", summary: "write: /wt/out.txt" },
];
const capsOn  = { approvals: true, attention: true };
const capsOff = { approvals: false, attention: true };

test("Allow fires onDecision(reqId, 'allow')", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDecision = vi.fn();
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOn, onDecision } });
  await waitFor(() => screen.getByRole("button", { name: /^allow$/i }));
  await fireEvent.click(screen.getByRole("button", { name: /^allow$/i }));
  expect(onDecision).toHaveBeenCalledWith("req_1", "allow");
});

test("batch Approve all calls onDecision for every queued item", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDecision = vi.fn();
  render(ApprovalCard, { props: { req: batchQueue[0], queue: batchQueue, caps: capsOn, onDecision } });
  await waitFor(() => screen.getByText(/2 pending/i));
  await fireEvent.click(screen.getByRole("button", { name: /approve all/i }));
  expect(onDecision).toHaveBeenCalledTimes(2);
  expect(onDecision).toHaveBeenCalledWith("req_1", "allow");
  expect(onDecision).toHaveBeenCalledWith("req_2", "allow");
});

test("hidden when Caps.approvals is false", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOff, onDecision: () => {} } });
  expect(screen.queryByRole("region", { name: /approval/i })).toBeNull();
});

test("batch buttons NOT rendered when queue has only one item", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOn, onDecision: vi.fn() } });
  await waitFor(() => screen.getByRole("button", { name: /^allow$/i }));
  expect(screen.queryByRole("button", { name: /approve all/i })).toBeNull();
  expect(screen.queryByRole("button", { name: /deny all/i })).toBeNull();
});

test("batch Approve all invokes onApproveAll prop when provided", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onApproveAll = vi.fn();
  const onDecision   = vi.fn();
  render(ApprovalCard, { props: { req: batchQueue[0], queue: batchQueue, caps: capsOn, onDecision, onApproveAll } });
  await waitFor(() => screen.getByRole("button", { name: /approve all/i }));
  await fireEvent.click(screen.getByRole("button", { name: /approve all/i }));
  expect(onApproveAll).toHaveBeenCalledTimes(1);
  expect(onDecision).not.toHaveBeenCalled();
});

test("batch Deny all invokes onDenyAll prop when provided", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDenyAll  = vi.fn();
  const onDecision = vi.fn();
  render(ApprovalCard, { props: { req: batchQueue[0], queue: batchQueue, caps: capsOn, onDecision, onDenyAll } });
  await waitFor(() => screen.getByRole("button", { name: /deny all/i }));
  await fireEvent.click(screen.getByRole("button", { name: /deny all/i }));
  expect(onDenyAll).toHaveBeenCalledTimes(1);
  expect(onDecision).not.toHaveBeenCalled();
});

// F27-fe: the card body renders a large tool input (no more approving blind).
test("renders the tool input text in the body for a large input", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const bigInput = "echo " + "x".repeat(200); // >= 120 bytes
  const req = { reqId: "req_1", tool: "Bash", summary: "Bash", input: bigInput };
  render(ApprovalCard, { props: { req, queue: [req], caps: capsOn, onDecision: vi.fn() } });
  const body = await screen.findByTestId("approval-input");
  expect(body.textContent).toContain(bigInput);
});

// F25: Allow gets focus on open (Enter approves), and the single key 'a' fires allow.
test("Allow is focused on mount and keydown 'a' fires allow", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDecision = vi.fn();
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOn, onDecision } });
  const allow = await screen.findByRole("button", { name: /^allow$/i });
  await waitFor(() => expect(document.activeElement).toBe(allow));
  await fireEvent.keyDown(allow, { key: "a" });
  expect(onDecision).toHaveBeenCalledWith("req_1", "allow");
});

// F25: 'd' denies. The risky "always" action needs the Shift+A chord, never a lone key.
test("keydown 'd' fires deny; plain 'a' never fires always", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDecision = vi.fn();
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOn, onDecision } });
  const allow = await screen.findByRole("button", { name: /^allow$/i });
  await fireEvent.keyDown(allow, { key: "d" });
  expect(onDecision).toHaveBeenCalledWith("req_1", "deny");
  expect(onDecision).not.toHaveBeenCalledWith("req_1", "always");
});

// F28: "Always allow" grants immediately, shows a post-grant undo toast, and Undo
// invokes the optional onUndoAlways hook.
test("Always allow grants then offers Undo which calls onUndoAlways", async () => {
  const { default: ApprovalCard } = await import("./ApprovalCard.svelte");
  const onDecision   = vi.fn();
  const onUndoAlways = vi.fn();
  render(ApprovalCard, { props: { req: singleReq, queue: [singleReq], caps: capsOn, onDecision, onUndoAlways } });
  await fireEvent.click(await screen.findByRole("button", { name: /always allow/i }));
  expect(onDecision).toHaveBeenCalledWith("req_1", "always");
  const undo = await screen.findByRole("button", { name: /^undo$/i });
  await fireEvent.click(undo);
  expect(onUndoAlways).toHaveBeenCalledWith("req_1");
});
