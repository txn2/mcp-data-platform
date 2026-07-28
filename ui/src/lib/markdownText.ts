// Ordered source-to-text rewrites applied by markdownToPlainText. Sequence
// matters: link and code constructs are unwrapped before the line-leading
// block markers, and the longest emphasis delimiters before the shorter ones
// so `**` is never read as two `*`.
const REWRITES: [RegExp, string][] = [
  // Fenced code blocks: drop the fences, keep the code as text.
  [/^\s*(?:```|~~~)[^\n]*$/gm, " "],
  // Images before links: the alt text is an image's only readable part.
  [/!\[([^\]]*)\]\([^)]*\)/g, "$1"],
  [/\[([^\]]*)\]\([^)]*\)/g, "$1"],
  // Autolinks keep the address.
  [/<((?:https?|mailto):[^>\s]+)>/g, "$1"],
  // Inline code keeps its contents.
  [/`+([^`\n]+)`+/g, "$1"],
  // Line-leading block markers.
  [/^\s{0,3}#{1,6}\s+/gm, ""],
  [/^\s*>+\s?/gm, ""],
  [/^\s*[-*+]\s+/gm, ""],
  [/^\s*\d+[.)]\s+/gm, ""],
  // A table's alignment row: pipes, dashes, colons and spaces only.
  [/^[\s:|-]*\|[\s:|-]*$/gm, " "],
  // Remaining table pipes become cell separators.
  [/\s*\|\s*/g, " "],
  // Horizontal rules and setext underlines carry no text.
  [/^\s*(?:[-*_=]\s*){3,}$/gm, " "],
  // Emphasis delimiters. As in CommonMark, the run must open on a non-space
  // and close on a non-space, so arithmetic ("2 * 3 * 4") keeps its operators.
  // The underscore and single-asterisk forms additionally require a word
  // boundary outside the run, so snake_case identifiers (is_outgoing,
  // legacy_order_id) survive intact.
  [/\*\*(\S[^*\n]*?\S|\S)\*\*/g, "$1"],
  [/~~(\S[^~\n]*?\S|\S)~~/g, "$1"],
  [/(^|[\s(])__(\S[^_\n]*?\S|\S)__(?=[\s).,!?:;]|$)/gm, "$1$2"],
  [/(^|[\s(])\*(\S[^*\n]*?\S|\S)\*(?=[\s).,!?:;]|$)/gm, "$1$2"],
  [/(^|[\s(])_(\S[^_\n]*?\S|\S)_(?=[\s).,!?:;]|$)/gm, "$1$2"],
  // Residual inline HTML.
  [/<\/?[a-zA-Z][^>]*>/g, ""],
];

// markdownToPlainText renders markdown source down to readable prose for
// places with no room to lay out markup: table cells, list rows, and clamped
// previews. Formatting is removed rather than displayed, so a description
// authored as markdown never shows its own asterisks and backticks to the
// reader. Surfaces with room render the real thing through MarkdownRenderer.
export function markdownToPlainText(md: string | null | undefined): string {
  if (!md) {
    return "";
  }
  let out = md;
  for (const [pattern, replacement] of REWRITES) {
    out = out.replace(pattern, replacement);
  }
  return out.replace(/\s+/g, " ").trim();
}
