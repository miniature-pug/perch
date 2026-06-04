// frontend/src/lib/highlight.ts
//
// Perch syntax highlight style — maps common lezer tags to perch CSS-variable
// tokens so token colors re-theme automatically across all 9 app themes.
//
// @lezer/highlight is NOT listed directly in package.json (it's a transitive
// dep of @codemirror/language, which is declared). It can't vanish from the
// lock-file as long as @codemirror/language is present, but note the hygiene
// gap if the tree is audited.
import { tags } from "@lezer/highlight";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";

/**
 * HighlightStyle that maps common syntax categories to perch CSS variables.
 *
 * Palette mapping (5 semantic hues + dim):
 *   --perch-accent  → keywords, punctuation/delimiter, meta
 *   --perch-ok      → strings, regexp, attribute values
 *   --perch-warn    → numbers, booleans, literals
 *   --perch-err     → type names, class names, tags
 *   --perch-text    → function/variable names, properties
 *   --perch-text-dim → comments, line comments, block comments
 */
export const perchHighlightStyle = HighlightStyle.define([
  // Keywords — accent (bright primary)
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
