// frontend/src/lib/ApprovalCard.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

const singleReq  = { reqId: "req_1", tool: "Bash",     summary: "run: ls -la /tmp" };
const batchQueue = [
  { reqId: "req_1", tool: "Bash",      summary: "run: ls -la /tmp" },
  { reqId: "req_2", tool: "WriteFile", summary: "write: /wt/out.txt" },
];
const capsOn  = { approvals: true, attention: true, tokens: true };
const capsOff = { approvals: false, attention: true, tokens: true };

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
