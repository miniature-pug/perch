import { render, screen, waitFor } from "@testing-library/svelte";
import { vi } from "vitest";

const sessions = [
  { id: "ses_a", session: "perch", window: "feat-x", paneId: "%1", status: "working", dir: "/wt/x" },
];
vi.mock("./wails", () => ({
  listSessions: vi.fn(async () => sessions),
  onSessionsChanged: vi.fn(() => () => {}),
}));

test("renders a row per session and subscribes to changes", async () => {
  const { default: Sidebar } = await import("./Sidebar.svelte");
  const w = await import("./wails");
  render(Sidebar);
  await waitFor(() => expect(screen.getByText("feat-x")).toBeInTheDocument());
  expect(w.onSessionsChanged).toHaveBeenCalled();
});

test("invokes onselect when a session is clicked", async () => {
  const { fireEvent } = await import("@testing-library/svelte");
  const { default: Sidebar } = await import("./Sidebar.svelte");
  let picked: any;
  render(Sidebar, { props: { onselect: (s: any) => (picked = s) } });
  await waitFor(() => screen.getByText("feat-x"));
  await fireEvent.click(screen.getByText("feat-x"));
  expect(picked?.id).toBe("ses_a");
});
