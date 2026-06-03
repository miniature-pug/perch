// frontend/src/lib/preview.test.ts
// Unit tests for the pure previewKind / isPreviewable helpers.
import { describe, it, expect } from "vitest";
import { isPreviewable, previewKind } from "./preview";

describe("isPreviewable", () => {
  it(".md → previewable", () => expect(isPreviewable("README.md")).toBe(true));
  it(".MD (uppercase) → previewable", () => expect(isPreviewable("README.MD")).toBe(true));
  it(".markdown → previewable", () => expect(isPreviewable("notes.markdown")).toBe(true));
  it(".mmd → previewable", () => expect(isPreviewable("arch.mmd")).toBe(true));
  it(".svg → previewable", () => expect(isPreviewable("icon.svg")).toBe(true));
  it(".png → previewable", () => expect(isPreviewable("logo.png")).toBe(true));
  it(".jpg → previewable", () => expect(isPreviewable("photo.jpg")).toBe(true));
  it("Dockerfile (no ext) → NOT previewable", () => expect(isPreviewable("Dockerfile")).toBe(false));
  it("foo (no ext) → NOT previewable", () => expect(isPreviewable("foo")).toBe(false));
  it("a.b.md (multi-dot) → previewable", () => expect(isPreviewable("a.b.md")).toBe(true));
  it("null → NOT previewable", () => expect(isPreviewable(null)).toBe(false));
  it(".ts → NOT previewable", () => expect(isPreviewable("main.ts")).toBe(false));
});

describe("previewKind", () => {
  it(".md → markdown", () => expect(previewKind("README.md")).toBe("markdown"));
  it(".MD (uppercase) → markdown", () => expect(previewKind("README.MD")).toBe("markdown"));
  it(".markdown → markdown", () => expect(previewKind("notes.markdown")).toBe("markdown"));
  it(".Markdown (mixed case) → markdown", () => expect(previewKind("README.Markdown")).toBe("markdown"));
  it(".mmd → mermaid", () => expect(previewKind("arch.mmd")).toBe("mermaid"));
  it(".svg → image", () => expect(previewKind("icon.svg")).toBe("image"));
  it(".png → image", () => expect(previewKind("logo.png")).toBe("image"));
  it(".jpg → image", () => expect(previewKind("photo.jpg")).toBe("image"));
  it("a.b.md (multi-dot) → markdown", () => expect(previewKind("a.b.md")).toBe("markdown"));
});
