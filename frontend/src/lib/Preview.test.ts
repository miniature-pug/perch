// frontend/src/lib/Preview.test.ts
import { render, screen, waitFor } from "@testing-library/svelte";
import { vi } from "vitest";

vi.mock("mermaid", () => ({ default: {
  initialize: vi.fn(),
  render: vi.fn(async () => ({ svg: "<svg></svg>" })),
}}));
vi.mock("marked", () => ({ marked: vi.fn(async (s: string) => `<p>${s}</p>`) }));

test("renders markdown", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/README.md", kind: "markdown", content: "# Hello" } });
  await waitFor(() => expect(screen.getByRole("region", { name: /preview/i })).toBeInTheDocument());
  expect(screen.getByRole("region", { name: /preview/i }).innerHTML).toContain("<p>");
});

test("renders mermaid", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/arch.mmd", kind: "mermaid", content: "graph TD; A-->B" } });
  await waitFor(() => expect(screen.getByRole("region", { name: /preview/i })).toBeInTheDocument());
  const m = await import("mermaid");
  expect(m.default.render).toHaveBeenCalled();
});

test("renders image", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/logo.png", kind: "image", content: "" } });
  await waitFor(() => screen.getByRole("img"));
  expect(screen.getByRole("img")).toHaveAttribute("src", "/wt/logo.png");
});

test("strips dangerous HTML from rendered markdown (no XSS)", async () => {
  const w = await import("marked");
  // Force marked to emit a malicious payload as if a markdown file contained raw HTML
  vi.mocked(w.marked).mockReturnValueOnce('<img src=x onerror="window.__xss=true"><p>safe</p>' as any);
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/readme.md", kind: "markdown", content: "irrelevant" } });
  await waitFor(() => expect(document.querySelector(".preview-body")).toBeInTheDocument());
  const body = document.querySelector(".preview-body")!;
  expect(body.querySelector("img[onerror]")).toBeNull();      // onerror stripped
  expect(body.innerHTML).not.toContain("onerror");
});
