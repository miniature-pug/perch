// frontend/src/lib/Preview.test.ts
import { render, screen, waitFor, fireEvent } from "@testing-library/svelte";
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

test("FEX-11: renders an image from the backend's /wt-file/ URL, not the filesystem path", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/logo.png", kind: "image", content: "", src: "/wt-file/ws-1/logo.png?v=0" } });
  await waitFor(() => screen.getByRole("img"));
  expect(screen.getByRole("img")).toHaveAttribute("src", "/wt-file/ws-1/logo.png?v=0");
});

test("FEX-11: an image with no served URL shows a note instead of a broken image", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/elsewhere/logo.png", kind: "image", content: "" } });
  expect(screen.queryByRole("img")).toBeNull();
  expect(screen.getByText(/cannot be previewed/)).toBeInTheDocument();
});

// --- F8b: the render side-effect is gated on `visible` (no work while off-screen) ---

test("does not render marked while hidden (visible=false), renders when shown", async () => {
  const { default: Preview } = await import("./Preview.svelte");
  const m = await import("marked");
  vi.mocked(m.marked).mockClear();

  // Mounted but off-screen: marked must not run even though content is present.
  const { rerender } = render(Preview, {
    props: { path: "/wt/a.md", kind: "markdown", content: "# Hi", visible: false },
  });
  await new Promise((r) => setTimeout(r, 20));
  expect(m.marked).not.toHaveBeenCalled();

  // Revealed: the effect re-runs and renders the current content.
  await rerender({ path: "/wt/a.md", kind: "markdown", content: "# Hi", visible: true });
  await waitFor(() => expect(m.marked).toHaveBeenCalled());
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

// --- Audit regressions: markdown can't restyle, overlay, or navigate the app (FEX-10) ---

test("FEX-10: rendered markdown drops style elements, style attributes, forms and inputs", async () => {
  const w = await import("marked");
  vi.mocked(w.marked).mockReturnValueOnce(
    "<style>.approval-card .btn-primary::after{content:'Deny'}</style>" +
    "<div style=\"position:fixed;inset:0;z-index:99999\" class=\"modal-overlay\">overlay</div>" +
    "<form action=\"https://e.example\"><input name=\"q\"><button>go</button></form>" +
    "<svg><style>*{display:none}</style></svg><p>safe</p>" as any,
  );
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/readme.md", kind: "markdown", content: "x" } });
  await waitFor(() => expect(document.querySelector(".preview-body p")).not.toBeNull());
  const body = document.querySelector(".preview-body")!;
  expect(body.querySelector("style")).toBeNull();
  expect(body.querySelector("[style]")).toBeNull();
  expect(body.querySelector("[class]")).toBeNull();
  expect(body.querySelector("form, input, button")).toBeNull();
  expect(body.textContent).toContain("overlay"); // the text stays, the styling goes
});

test("FEX-10: an external link opens in the system browser, never navigates the app", async () => {
  const w = await import("marked");
  vi.mocked(w.marked).mockReturnValueOnce('<p><a href="https://example.com/docs">docs</a></p>' as any);
  const open = vi.fn();
  (window as any).runtime = { ...(window as any).runtime, BrowserOpenURL: open };
  try {
    const { default: Preview } = await import("./Preview.svelte");
    render(Preview, { props: { path: "/wt/readme.md", kind: "markdown", content: "x" } });
    const link = await screen.findByRole("link", { name: "docs" });
    const ev = new MouseEvent("click", { bubbles: true, cancelable: true });
    link.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(open).toHaveBeenCalledWith("https://example.com/docs");
  } finally {
    delete (window as any).runtime.BrowserOpenURL;
  }
});

test("FEX-10: a relative link opens the file it names; a javascript: link does nothing", async () => {
  const w = await import("marked");
  vi.mocked(w.marked).mockReturnValueOnce(
    '<p><a href="../docs/usage.md#setup">usage</a> <a href="javascript:alert(1)">bad</a></p>' as any);
  const onOpenFile = vi.fn();
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/src/readme.md", kind: "markdown", content: "x", onOpenFile } });
  await fireEvent.click(await screen.findByRole("link", { name: "usage" }));
  expect(onOpenFile).toHaveBeenCalledWith("/wt/docs/usage.md");
  const bad = screen.queryByText("bad");
  if (bad) {
    const ev = new MouseEvent("click", { bubbles: true, cancelable: true });
    bad.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
  }
  expect(onOpenFile).toHaveBeenCalledTimes(1);
});

test("FEX-25: every mermaid render gets its own id", async () => {
  const m = await import("mermaid");
  vi.mocked(m.default.render).mockClear();
  const { default: Preview } = await import("./Preview.svelte");
  const r = render(Preview, { props: { path: "/wt/a.mmd", kind: "mermaid", content: "graph TD; A-->B" } });
  await waitFor(() => expect(m.default.render).toHaveBeenCalledTimes(1));
  await r.rerender({ path: "/wt/a.mmd", kind: "mermaid", content: "graph TD; A-->C" });
  await waitFor(() => expect(m.default.render).toHaveBeenCalledTimes(2));
  const ids = vi.mocked(m.default.render).mock.calls.map((c) => c[0]);
  expect(new Set(ids).size).toBe(2);
});

test("FEX-23: markdown offers an Edit source button when the host supports it", async () => {
  const onEditSource = vi.fn();
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/a.md", kind: "markdown", content: "# a", onEditSource } });
  await fireEvent.click(screen.getByRole("button", { name: /edit source/i }));
  expect(onEditSource).toHaveBeenCalled();
});

test("review #6: task-list checkboxes survive (disabled); other inputs do not", async () => {
  const w = await import("marked");
  vi.mocked(w.marked).mockReturnValueOnce(
    '<ul><li><input checked="" type="checkbox"> done</li><li><input type="checkbox"> open</li></ul>' +
    '<input type="text" value="phish"><input type="password">' as any);
  const { default: Preview } = await import("./Preview.svelte");
  render(Preview, { props: { path: "/wt/todo.md", kind: "markdown", content: "x" } });
  await waitFor(() => expect(document.querySelector(".preview-body li")).not.toBeNull());
  const boxes = Array.from(document.querySelectorAll(".preview-body input")) as HTMLInputElement[];
  expect(boxes.map((b) => b.type)).toEqual(["checkbox", "checkbox"]);
  expect(boxes.map((b) => b.checked)).toEqual([true, false]);
  expect(boxes.every((b) => b.disabled)).toBe(true);
});

test("review #7: percent-encoded .. cannot escape through a relative link", async () => {
  const { resolveRelative } = await import("./preview");
  expect(resolveRelative("/wt/docs/a.md", "%2e%2e/%2e%2e/other/x")).toBe("/other/x");
  expect(resolveRelative("/wt/docs/a.md", "..%2F..%2Fother%2Fx")).toBeNull();
  expect(resolveRelative("/wt/docs/a.md", "a%00b.md")).toBeNull();
  expect(resolveRelative("/wt/docs/a.md", "my%20notes.md")).toBe("/wt/docs/my notes.md");
  expect(resolveRelative("/wt/docs/a.md", "%E0%A4%A")).toBeNull();
});
