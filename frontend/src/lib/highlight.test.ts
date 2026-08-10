// frontend/src/lib/highlight.test.ts
//
// Unit tests for the perch HighlightStyle.
// These tests use highlightingFor() from @codemirror/language to probe which CSS class
// the style assigns to given tags. They need no rendered DOM or CM layout.
import { describe, it, expect } from "vitest";
import { tags } from "@lezer/highlight";
import { highlightingFor } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { perchHighlightStyle, perchSyntaxHighlighting } from "./highlight";

// Build a minimal EditorState that includes the highlighting extension so
// highlightingFor() can walk the facet.
function stateWithHighlighting(): EditorState {
  return EditorState.create({
    doc: "",
    extensions: [perchSyntaxHighlighting],
  });
}

describe("perchHighlightStyle — tag coverage", () => {
  // highlightingFor returns a space-separated class string or null when the
  // style has no mapping for that tag. Non-null means the style emitted a CSS class.

  it("assigns a class to keyword tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.keyword])).not.toBeNull();
  });

  it("assigns a class to string tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.string])).not.toBeNull();
  });

  it("assigns a class to comment tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.comment])).not.toBeNull();
  });

  it("assigns a class to number tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.number])).not.toBeNull();
  });

  it("assigns a class to typeName tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.typeName])).not.toBeNull();
  });

  it("assigns a class to operator tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.operator])).not.toBeNull();
  });

  it("assigns a class to variableName tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.variableName])).not.toBeNull();
  });

  it("assigns a class to propertyName tags", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.propertyName])).not.toBeNull();
  });

  it("assigns distinct classes to keyword and string (different hues)", () => {
    const state = stateWithHighlighting();
    const kw  = highlightingFor(state, [tags.keyword]);
    const str = highlightingFor(state, [tags.string]);
    // Both values must be present and must differ. This proves the style maps them to different CSS classes.
    expect(kw).not.toBeNull();
    expect(str).not.toBeNull();
    expect(kw).not.toBe(str);
  });

  it("assigns a class to bool/null literals", () => {
    const state = stateWithHighlighting();
    expect(highlightingFor(state, [tags.bool])).not.toBeNull();
    expect(highlightingFor(state, [tags.null])).not.toBeNull();
  });
});

describe("perchHighlightStyle — CSS variable references", () => {
  it("style spec entries reference var(--perch-*) colors", () => {
    // Introspect the HighlightStyle's specs array to confirm it uses CSS-variable tokens.
    // This guards against accidental hard-coded hex replacements.
    const specs = (perchHighlightStyle as any).specs as Array<{ color?: string }>;
    const hasVarColor = specs.some(
      (s) => typeof s.color === "string" && s.color.startsWith("var(--perch-")
    );
    expect(hasVarColor).toBe(true);
  });
});
