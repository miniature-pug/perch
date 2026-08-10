// frontend/src/lib/highlight.ts
//
// The perch syntax highlight style. It maps common lezer tags to perch
// CSS-variable tokens, so token colors re-theme automatically across all 9
// app themes.
//
// @lezer/highlight is not listed directly in package.json. It is a
// transitive dependency of @codemirror/language, which is declared. It
// cannot vanish from the lock file as long as @codemirror/language is
// present, but note this hygiene gap if the dependency tree is audited.
import { tags } from "@lezer/highlight";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";

/**
 * HighlightStyle that maps common syntax categories to perch CSS variables.
 *
 * Palette mapping (5 semantic hues, plus dim):
 *   --perch-accent   maps to keywords, punctuation and delimiters, meta.
 *   --perch-ok       maps to strings, regexp, attribute values.
 *   --perch-warn     maps to numbers, booleans, literals.
 *   --perch-err      maps to type names, class names, tags.
 *   --perch-text     maps to function and variable names, properties.
 *   --perch-text-dim maps to comments: line comments and block comments.
 */
export const perchHighlightStyle = HighlightStyle.define([
  // Keywords: accent (bright primary)
  { tag: tags.keyword,            color: "var(--perch-accent)", fontWeight: "bold" },
  { tag: tags.controlKeyword,     color: "var(--perch-accent)", fontWeight: "bold" },
  { tag: tags.moduleKeyword,      color: "var(--perch-accent)", fontWeight: "bold" },
  { tag: tags.operatorKeyword,    color: "var(--perch-accent)" },
  // Operators / punctuation
  { tag: tags.operator,           color: "var(--perch-accent)" },
  { tag: tags.punctuation,        color: "var(--perch-text)" },
  { tag: tags.separator,          color: "var(--perch-text)" },
  { tag: tags.bracket,            color: "var(--perch-text)" },
  // Strings
  { tag: tags.string,             color: "var(--perch-ok)" },
  { tag: tags.regexp,             color: "var(--perch-ok)", fontStyle: "italic" },
  { tag: tags.special(tags.string), color: "var(--perch-ok)" },
  // Numbers & booleans (literals)
  { tag: tags.number,             color: "var(--perch-warn)" },
  { tag: tags.integer,            color: "var(--perch-warn)" },
  { tag: tags.float,              color: "var(--perch-warn)" },
  { tag: tags.bool,               color: "var(--perch-warn)" },
  { tag: tags.null,               color: "var(--perch-warn)" },
  // Types / classes
  { tag: tags.typeName,           color: "var(--perch-err)" },
  { tag: tags.className,          color: "var(--perch-err)" },
  { tag: tags.namespace,          color: "var(--perch-err)" },
  { tag: tags.tagName,            color: "var(--perch-err)" },
  { tag: tags.angleBracket,       color: "var(--perch-err)" },
  // Function / variable names
  { tag: tags.function(tags.variableName), color: "var(--perch-text)", fontWeight: "bold" },
  { tag: tags.variableName,       color: "var(--perch-text)" },
  { tag: tags.definition(tags.variableName), color: "var(--perch-text)" },
  { tag: tags.propertyName,       color: "var(--perch-text)" },
  { tag: tags.function(tags.propertyName), color: "var(--perch-text)", fontWeight: "bold" },
  // Comments
  { tag: tags.comment,            color: "var(--perch-text-dim)", fontStyle: "italic" },
  { tag: tags.lineComment,        color: "var(--perch-text-dim)", fontStyle: "italic" },
  { tag: tags.blockComment,       color: "var(--perch-text-dim)", fontStyle: "italic" },
  { tag: tags.docComment,         color: "var(--perch-text-dim)", fontStyle: "italic" },
  // Meta / special
  { tag: tags.meta,               color: "var(--perch-accent)" },
  { tag: tags.atom,               color: "var(--perch-warn)" },
  { tag: tags.self,               color: "var(--perch-accent)" },
  { tag: tags.special(tags.variableName), color: "var(--perch-accent)" },
  // Attribute names (HTML/CSS)
  { tag: tags.attributeName,      color: "var(--perch-ok)" },
  { tag: tags.attributeValue,     color: "var(--perch-ok)" },
  // Heading (Markdown)
  { tag: tags.heading,            color: "var(--perch-accent)", fontWeight: "bold" },
  { tag: tags.emphasis,           fontStyle: "italic" },
  { tag: tags.strong,             fontWeight: "bold" },
  { tag: tags.link,               color: "var(--perch-ok)", textDecoration: "underline" },
  // Invalid / error
  { tag: tags.invalid,            color: "var(--perch-err)" },
]);

/** Drop-in extension: `syntaxHighlighting(perchHighlightStyle)` */
export const perchSyntaxHighlighting = syntaxHighlighting(perchHighlightStyle);
