import { describe, it, expect } from "vitest";

describe("CodeMirror smoke", () => {
  it("EditorView can be constructed in jsdom", async () => {
    const { EditorView } = await import("@codemirror/view");
    const { EditorState } = await import("@codemirror/state");
    const div = document.createElement("div");
    document.body.appendChild(div);
    const view = new EditorView({
      state: EditorState.create({ doc: "hello" }),
      parent: div,
    });
    expect(view.state.doc.toString()).toBe("hello");
    view.destroy();
    document.body.removeChild(div);
  });
});
