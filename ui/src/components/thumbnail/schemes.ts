/**
 * The two color schemes a DOM-drawn tile is drawn in, and the CSS each content
 * family is drawn with.
 *
 * These live apart from the tile page because they are data rather than
 * behavior, and every family the tile page grows adds a stylesheet here.
 *
 * All prose CSS is scoped to a caller-supplied `scope` class rather than a
 * shared class name, so a tile's styles cannot reach anything else on the page
 * it is drawn on.
 */

import type { ThumbnailVariant } from "@/lib/thumbnail";

/** Color tokens for one thumbnail color scheme. */
export interface ProseTokens {
  bg: string;
  fg: string;
  codeBg: string;
  border: string;
  blockquoteBorder: string;
  muted: string;
  link: string;
  thBg: string;
  evenRow: string;
  /**
   * Syntax tones for the JSON families. They mirror the JsonTree palette
   * (renderers/json/JsonTree.tsx) so a thumbnail reads as the same document the
   * viewer shows, as concrete hex so a tile does not depend on which of the
   * viewer's custom properties the tile page happens to define.
   */
  jsonKey: string;
  jsonString: string;
  jsonNumber: string;
  jsonLiteral: string;
}

export interface Scheme {
  variant: ThumbnailVariant;
  mermaidTheme: "default" | "dark";
  tokens: ProseTokens;
}

export const LIGHT_SCHEME: Scheme = {
  variant: "light",
  mermaidTheme: "default",
  tokens: {
    bg: "#ffffff",
    fg: "#111827",
    codeBg: "#f3f4f6",
    border: "#d1d5db",
    blockquoteBorder: "#d1d5db",
    muted: "#6b7280",
    link: "#2563eb",
    thBg: "#f1f5f9",
    evenRow: "#f8fafc",
    jsonKey: "#0369a1",
    jsonString: "#047857",
    jsonNumber: "#6d28d9",
    jsonLiteral: "#b45309",
  },
};

// Dark tokens mirror the portal's shadcn dark palette so the drawn tile blends
// into the dark card. They are concrete colors; keep them in sync with the
// `.dark` block in src/index.css (--card -> #131a25, --card-foreground ->
// #f8fafc, --border/--muted/...) if that palette changes. A changed value here
// reaches existing tiles only when they are drawn again.
export const DARK_SCHEME: Scheme = {
  variant: "dark",
  mermaidTheme: "dark",
  tokens: {
    bg: "#131a25",
    fg: "#f8fafc",
    codeBg: "#1e293b",
    border: "#334155",
    blockquoteBorder: "#475569",
    muted: "#94a3b8",
    link: "#60a5fa",
    thBg: "#1e293b",
    evenRow: "#0f172a",
    jsonKey: "#7dd3fc",
    jsonString: "#6ee7b7",
    jsonNumber: "#c4b5fd",
    jsonLiteral: "#fbbf24",
  },
};

/** The monospace stack the code-shaped families are drawn in. */
const MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";

// tableBaseCss is the table border/spacing shared by the tabular variants; each
// adds its own cell sizing on top.
function tableBaseCss(t: ProseTokens, scope: string): string {
  return `
    .${scope} table { border-collapse: collapse; margin: 0.4em 0; }
    .${scope} th, .${scope} td { border: 1px solid ${t.border}; padding: 0.25em 0.5em; }`;
}

export function markdownProseCss(t: ProseTokens, scope: string): string {
  return `
    .${scope} h1 { font-size: 1.5em; font-weight: 700; margin: 0.5em 0 0.25em; }
    .${scope} h2 { font-size: 1.25em; font-weight: 600; margin: 0.5em 0 0.25em; }
    .${scope} h3 { font-size: 1.1em; font-weight: 600; margin: 0.4em 0 0.2em; }
    .${scope} p { margin: 0.4em 0; }
    .${scope} ul, .${scope} ol { padding-left: 1.5em; margin: 0.4em 0; }
    .${scope} code { background: ${t.codeBg}; padding: 0.1em 0.3em; border-radius: 3px; font-size: 0.9em; }
    .${scope} pre { background: ${t.codeBg}; padding: 0.5em; border-radius: 4px; overflow: auto; margin: 0.4em 0; }
    .${scope} blockquote { border-left: 3px solid ${t.blockquoteBorder}; padding-left: 0.75em; margin: 0.4em 0; color: ${t.muted}; }
    .${scope} a { color: ${t.link}; text-decoration: underline; }
    ${tableBaseCss(t, scope)}
    .${scope} th, .${scope} td { font-size: 0.9em; }
  `;
}

export function csvProseCss(t: ProseTokens, scope: string): string {
  return `
    ${tableBaseCss(t, scope)}
    .${scope} table { width: 100%; }
    .${scope} th, .${scope} td { font-size: 0.85em; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 120px; }
    .${scope} th { background: ${t.thBg}; font-weight: 600; }
    .${scope} tr:nth-child(even) { background: ${t.evenRow}; }
  `;
}

/**
 * Plain text: the file as it reads, in the body font rather than a monospace
 * one -- a .txt is prose far more often than it is code, and the families that
 * are code (JSON, NDJSON) have stylesheets of their own.
 *
 * Long lines wrap instead of running off the right edge, because a plain-text
 * file has no structure to tell the reader what they are looking at: the first
 * few sentences are the whole of what the tile can say, and clipping them at
 * the container edge would throw away half of each one.
 */
export function textProseCss(t: ProseTokens, scope: string): string {
  return `
    .${scope} { margin: 0; font-family: inherit; font-size: 11px; line-height: 1.55; color: ${t.fg}; white-space: pre-wrap; overflow-wrap: break-word; }
  `;
}

/**
 * Formatted JSON: an indented monospace document with the punctuation left in
 * the muted tone so the values it separates carry the eye.
 */
export function jsonProseCss(t: ProseTokens, scope: string): string {
  return `
    .${scope} { font-family: ${MONO}; font-size: 10px; line-height: 1.45; margin: 0; white-space: pre; color: ${t.muted}; }
    .${scope} .jt-key { color: ${t.jsonKey}; }
    .${scope} .jt-string { color: ${t.jsonString}; }
    .${scope} .jt-number { color: ${t.jsonNumber}; }
    .${scope} .jt-literal { color: ${t.jsonLiteral}; font-style: italic; }
  `;
}

/**
 * Newline-delimited JSON: one record per row behind a line-number gutter, the
 * shape its viewer lists them in. `table-layout: fixed` is what holds the
 * gutter to its width and keeps a long record from stretching the table into a
 * layout of its own; the record itself is left to run off the right edge, where
 * the tile's container clips it.
 */
export function ndjsonProseCss(t: ProseTokens, scope: string): string {
  return `
    .${scope} { font-family: ${MONO}; font-size: 10px; line-height: 1.7; color: ${t.muted}; }
    .${scope} table { table-layout: fixed; width: 100%; border-collapse: collapse; }
    .${scope} td { padding: 0; vertical-align: top; }
    .${scope} td.jt-line { width: 24px; padding-right: 6px; text-align: right; color: ${t.muted}; }
    .${scope} .jt-record { white-space: nowrap; }
    .${scope} .jt-key { color: ${t.jsonKey}; }
    .${scope} .jt-string { color: ${t.jsonString}; }
    .${scope} .jt-number { color: ${t.jsonNumber}; }
    .${scope} .jt-literal { color: ${t.jsonLiteral}; font-style: italic; }
  `;
}
