// A script's description is a document (#1369): markdown, at whatever length
// the automation needs explaining at. Most surfaces render it as one — the
// script's own page does — but a LIST row has one line for it, and dropping
// markdown into that line prints the syntax ("## What it produces") and lets a
// twenty-line document set the height of a queue row.
//
// descriptionSummary is the one line such a row shows: the first sentence-ish
// piece of prose in the document, with the markup a reader does not want to see
// removed. It is not a markdown renderer and does not try to be — it answers
// "what is this script, in a line", which is what a row asks.

// SUMMARY_MAX_CHARS bounds the line. It is generous enough for a full opening
// sentence and short enough that a row stays a row.
const SUMMARY_MAX_CHARS = 180;

/** descriptionSummary renders a markdown description as one line of prose. */
export function descriptionSummary(description: string): string {
  const line = firstProseLine(description);
  if (line.length <= SUMMARY_MAX_CHARS) {
    return line;
  }
  return line.slice(0, SUMMARY_MAX_CHARS).trimEnd() + "…";
}

// firstProseLine is the first line carrying prose: headings, fenced code, list
// bullets, quotes and horizontal rules are structure rather than the answer to
// "what is this". A document that is nothing BUT structure falls back to the
// first non-empty line, stripped, because saying something is better than
// saying nothing.
function firstProseLine(description: string): string {
  let inFence = false;
  let fallback = "";
  for (const raw of description.split("\n")) {
    const line = raw.trim();
    if (line.startsWith("```") || line.startsWith("~~~")) {
      inFence = !inFence;
      continue;
    }
    if (inFence || line === "") {
      continue;
    }
    const stripped = stripInline(line);
    if (stripped === "") {
      continue;
    }
    if (!isStructure(line)) {
      return stripped;
    }
    fallback ||= stripInline(line.replace(/^[#>\-*+\s]+/, ""));
  }
  return fallback;
}

// isStructure reports whether a line is markdown scaffolding rather than the
// prose a reader wants: a heading, a quote, a list item, or a rule.
function isStructure(line: string): boolean {
  return /^(#{1,6}\s|>\s?|[-*+]\s|\d+\.\s|(-{3,}|\*{3,}|_{3,})$)/.test(line);
}

// stripInline removes the inline markers that would otherwise show as
// punctuation in a plain-text line: emphasis, code ticks, and a link's target,
// keeping the text the link was written on.
function stripInline(line: string): string {
  return line
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/[*_`]/g, "")
    .trim();
}
