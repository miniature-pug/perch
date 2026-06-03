// frontend/src/lib/NewSessionDialog.test.ts  (replace existing)
import { render, screen, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";
import { vi } from "vitest";

test("onCreate fires with agent, repo, branch, model", async () => {
  const { default: NewSessionDialog } = await import("./NewSessionDialog.svelte");
  const onCreate = vi.fn();
  render(NewSessionDialog, { props: {
    open: true, repos: ["/home/user/proj"], branches: ["main", "feat/x"],
    onCreate, onClose: () => {},
  }});
  await waitFor(() => screen.getByRole("dialog", { name: /new session/i }));
  await fireEvent.change(screen.getByLabelText(/repo/i),   { target: { value: "/home/user/proj" } });
  await fireEvent.change(screen.getByLabelText(/branch/i), { target: { value: "feat/x" } });
  await fireEvent.change(screen.getByLabelText(/model/i),  { target: { value: "claude-opus-4-5" } });
  await fireEvent.click(screen.getByRole("button", { name: /create/i }));
  expect(onCreate).toHaveBeenCalledWith("claude", "/home/user/proj", "feat/x", "claude-opus-4-5");
});
