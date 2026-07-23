/**
 * The renderer registry: the single place that decides how a piece of content
 * is presented.
 *
 * Every surface that shows asset or resource content (the portal viewer, the
 * public/guest viewer, collection items, the resources detail modal) resolves
 * through this table, so a content type renders identically wherever it is
 * opened and a new family is added in one place rather than four.
 *
 * Each entry carries more than a component name, because the family determines
 * things the component cannot decide for itself: whether the source editor is
 * offered, whether the bytes are embedded in the page or streamed from a URL,
 * and how large a document may be before the viewer stops loading it inline.
 */

import { CT, resolveContentType, normalizeContentType } from "@/lib/contentType";

/** The renderer components a family can resolve to. */
export type RendererKind =
  | "json"
  | "ndjson"
  | "table"
  | "image"
  | "audio"
  | "video"
  | "pdf"
  | "markdown"
  | "html"
  | "jsx"
  | "svg"
  | "code"
  | "text"
  | "binary";

/** CodeMirror language modes available to the code renderer and source editor. */
export type CodeLanguage =
  | "json"
  | "yaml"
  | "xml"
  | "sql"
  | "python"
  | "javascript"
  | "html"
  | "markdown";

/**
 * How the viewer obtains the content.
 *
 * `inline` means the bytes are already in hand as a string. `url` means the
 * renderer points an element at the content endpoint: the only workable option
 * for binary families, which cannot survive being embedded in a page as text,
 * and the only one that supports seeking for audio and video.
 */
export type ContentSource = "inline" | "url";

export interface RendererEntry {
  kind: RendererKind;
  /** True when the family can be opened in the source editor. */
  editable: boolean;
  source: ContentSource;
  /** CodeMirror mode for the code renderer and the source editor. */
  language?: CodeLanguage;
  /**
   * Largest document the viewer will load inline, in bytes. `null` means the
   * renderer virtualizes and has no cutoff; `url`-sourced families ignore it.
   */
  inlineLimit: number | null;
  /** The delimiter for tabular families. */
  delimiter?: "," | "\t";
}

/**
 * Default inline cutoff for families rendered as one continuous block of text.
 * Above this the viewer offers a download instead, because the browser has to
 * lay out the whole document at once.
 */
export const TEXT_INLINE_LIMIT = 2 * 1024 * 1024; // 2 MB

/**
 * Cutoff for families whose renderers virtualize. These stay responsive on
 * documents far past the text limit because only the visible rows are in the
 * DOM, so the cap exists to bound parse time and memory, not layout.
 */
export const VIRTUALIZED_INLINE_LIMIT = 32 * 1024 * 1024; // 32 MB

const TEXT_ENTRY: RendererEntry = {
  kind: "text",
  editable: true,
  source: "inline",
  inlineLimit: TEXT_INLINE_LIMIT,
};

const BINARY_ENTRY: RendererEntry = {
  kind: "binary",
  editable: false,
  source: "url",
  inlineLimit: null,
};

const JSON_ENTRY: RendererEntry = {
  kind: "json",
  editable: true,
  source: "inline",
  language: "json",
  inlineLimit: VIRTUALIZED_INLINE_LIMIT,
};

const XML_ENTRY: RendererEntry = {
  kind: "code",
  editable: true,
  source: "inline",
  language: "xml",
  inlineLimit: TEXT_INLINE_LIMIT,
};

const REGISTRY: Record<string, RendererEntry> = {
  [CT.json]: JSON_ENTRY,
  [CT.ndjson]: {
    kind: "ndjson",
    editable: true,
    source: "inline",
    language: "json",
    inlineLimit: VIRTUALIZED_INLINE_LIMIT,
  },
  [CT.csv]: {
    kind: "table",
    editable: true,
    source: "inline",
    inlineLimit: VIRTUALIZED_INLINE_LIMIT,
    delimiter: ",",
  },
  [CT.tsv]: {
    kind: "table",
    editable: true,
    source: "inline",
    inlineLimit: VIRTUALIZED_INLINE_LIMIT,
    delimiter: "\t",
  },
  [CT.markdown]: { kind: "markdown", editable: true, source: "inline", language: "markdown", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.html]: { kind: "html", editable: true, source: "inline", language: "html", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.jsx]: { kind: "jsx", editable: true, source: "inline", language: "javascript", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.svg]: { kind: "svg", editable: true, source: "inline", language: "xml", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.xml]: XML_ENTRY,
  [CT.yaml]: { kind: "code", editable: true, source: "inline", language: "yaml", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.sql]: { kind: "code", editable: true, source: "inline", language: "sql", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.python]: { kind: "code", editable: true, source: "inline", language: "python", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.javascript]: { kind: "code", editable: true, source: "inline", language: "javascript", inlineLimit: TEXT_INLINE_LIMIT },
  "text/css": { kind: "code", editable: true, source: "inline", inlineLimit: TEXT_INLINE_LIMIT },
  [CT.plain]: TEXT_ENTRY,
  [CT.pdf]: { kind: "pdf", editable: false, source: "url", inlineLimit: null },
};

/** Media families resolved by their type prefix rather than an exact match. */
const PREFIX_KINDS: Array<{ prefix: string; entry: RendererEntry }> = [
  { prefix: "image/", entry: { kind: "image", editable: false, source: "url", inlineLimit: null } },
  { prefix: "audio/", entry: { kind: "audio", editable: false, source: "url", inlineLimit: null } },
  { prefix: "video/", entry: { kind: "video", editable: false, source: "url", inlineLimit: null } },
];

export interface ResolveInput {
  contentType: string;
  fileName?: string;
  /**
   * The content itself, when the caller already holds it. Supplying it lets the
   * registry fall back to content detection for assets stored under a generic
   * type before the server settled types at write time.
   */
  content?: string;
}

export interface Resolution extends RendererEntry {
  /** The canonical type the content resolved to. */
  contentType: string;
}

/**
 * Resolves the renderer for a piece of content. The declared type wins when it
 * is specific; otherwise the filename and then the content itself are consulted,
 * under the same passive-only rule the server applies.
 */
export function resolveRenderer(input: ResolveInput): Resolution {
  const contentType = resolveContentType(input.contentType, input.fileName, input.content);

  const exact = REGISTRY[contentType];
  if (exact) return { ...exact, contentType };

  for (const { prefix, entry } of PREFIX_KINDS) {
    if (contentType.startsWith(prefix)) return { ...entry, contentType };
  }

  // A structured suffix names the syntax even when the full type is
  // unregistered, as with application/vnd.acme.report+json.
  if (contentType.endsWith("+json")) return { ...JSON_ENTRY, contentType };
  if (contentType.endsWith("+xml")) return { ...XML_ENTRY, contentType };
  if (contentType.startsWith("text/")) return { ...TEXT_ENTRY, contentType };

  return { ...BINARY_ENTRY, contentType };
}

/**
 * The CodeMirror language for a content type, for the source editor. Returns
 * undefined when the type has no mode, in which case the editor runs plain.
 */
export function languageForContentType(contentType: string, fileName?: string): CodeLanguage | undefined {
  return resolveRenderer({ contentType, fileName }).language;
}

/**
 * True when content of this type can be opened in the source editor. Replaces
 * the old `isTextContent` substring test: editability is a property of the
 * family, not of whether its type string happens to contain "text".
 */
export function isEditableContent(contentType: string, fileName?: string): boolean {
  return resolveRenderer({ contentType, fileName }).editable;
}

/** True when the family renders from a content URL rather than embedded bytes. */
export function rendersFromURL(contentType: string, fileName?: string): boolean {
  return resolveRenderer({ contentType, fileName }).source === "url";
}

/**
 * True when a document of this size exceeds its family's inline cutoff and the
 * viewer should offer a download instead of loading it.
 */
export function exceedsInlineLimit(contentType: string, sizeBytes: number, fileName?: string): boolean {
  const entry = resolveRenderer({ contentType, fileName });
  if (entry.source === "url" || entry.inlineLimit === null) return false;
  return sizeBytes > entry.inlineLimit;
}

/** A short, human-readable name for a content type, for metadata displays. */
export function familyLabel(contentType: string): string {
  const n = normalizeContentType(contentType);
  const labels: Record<string, string> = {
    [CT.json]: "JSON",
    [CT.ndjson]: "JSON Lines",
    [CT.csv]: "CSV",
    [CT.tsv]: "TSV",
    [CT.xml]: "XML",
    [CT.yaml]: "YAML",
    [CT.markdown]: "Markdown",
    [CT.html]: "HTML",
    [CT.jsx]: "React component",
    [CT.svg]: "SVG",
    [CT.pdf]: "PDF",
    [CT.plain]: "Plain text",
    [CT.octet]: "Binary",
  };
  if (labels[n]) return labels[n];
  if (n.startsWith("image/")) return `Image (${n.slice(6).toUpperCase()})`;
  if (n.startsWith("audio/")) return `Audio (${n.slice(6).toUpperCase()})`;
  if (n.startsWith("video/")) return `Video (${n.slice(6).toUpperCase()})`;
  return n || "Unknown";
}
