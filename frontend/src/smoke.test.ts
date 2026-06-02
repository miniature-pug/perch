import { render, screen, waitFor } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("./lib/wails", () => ({
  listSessions: vi.fn(async () => []),
  onSessionsChanged: vi.fn(() => () => {}),
  createAgent: vi.fn(async () => "ses_new"),
  killSession: vi.fn(async () => {}),
}));

test("App mounts and renders the sessions nav", async () => {
  const { default: App } = await import("./App.svelte");
  render(App);
  await waitFor(() =>
    expect(screen.getByRole("navigation", { name: "sessions" })).toBeInTheDocument()
  );
});
