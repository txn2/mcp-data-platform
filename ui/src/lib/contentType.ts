/**
 * Client-side media-type normalization and detection.
 *
 * This mirrors the server's `pkg/contenttype` package. The server settles the
 * type at write time, but assets stored before that existed still carry
 * whatever generic type they were saved with, and re-typing them would need a
 * data migration. Running the same rules in the viewer means those assets get
 * the right renderer without one.
 *
 * The active-type rule is the same here as on the server: detection may only
 * ever land on a passive family. Content that looks like HTML, JSX or SVG but
 * was not declared as such renders as text, never as markup.
 */

/** Canonical media types, one per family. */
export const CT = {
  json: "application/json",
  ndjson: "application/x-ndjson",
  csv: "text/csv",
  tsv: "text/tab-separated-values",
  xml: "application/xml",
  yaml: "application/yaml",
  markdown: "text/markdown",
  plain: "text/plain",
  html: "text/html",
  jsx: "text/jsx",
  svg: "image/svg+xml",
  javascript: "text/javascript",
  sql: "application/sql",
  python: "text/x-python",
  pdf: "application/pdf",
  octet: "application/octet-stream",
} as const;

const ALIASES: Record<string, string> = {
  "text/json": CT.json,
  "application/x-json": CT.json,
  "application/ld+json": CT.json,
  "application/vnd.api+json": CT.json,
  "application/problem+json": CT.json,
  "application/ndjson": CT.ndjson,
  "text/x-ndjson": CT.ndjson,
  "application/jsonl": CT.ndjson,
  "text/xml": CT.xml,
  "application/x-xml": CT.xml,
  "text/yaml": CT.yaml,
  "text/x-yaml": CT.yaml,
  "application/x-yaml": CT.yaml,
  "application/csv": CT.csv,
  "text/comma-separated-values": CT.csv,
  "text/tsv": CT.tsv,
  "text/x-tsv": CT.tsv,
  "text/x-markdown": CT.markdown,
  "application/markdown": CT.markdown,
  "application/javascript": CT.javascript,
  "application/x-javascript": CT.javascript,
  "text/x-jsx": CT.jsx,
  "application/jsx": CT.jsx,
  "text/babel": CT.jsx,
  "text/react": CT.jsx,
  "application/svg+xml": CT.svg,
  "binary/octet-stream": CT.octet,
  "application/binary": CT.octet,
  "application/x-sql": CT.sql,
  "text/x-sql": CT.sql,
  "text/x-python-script": CT.python,
  "application/x-python-code": CT.python,
  "image/jpg": "image/jpeg",
  "image/x-png": "image/png",
  "audio/mp3": "audio/mpeg",
  "audio/x-wav": "audio/wav",
  "audio/wave": "audio/wav",
  "audio/x-flac": "audio/flac",
  "audio/x-m4a": "audio/mp4",
  "video/x-m4v": "video/mp4",
  "application/x-zip-compressed": "application/zip",
};

const ACTIVE = new Set<string>([CT.html, CT.jsx, CT.svg, CT.javascript]);

const GENERIC = new Set<string>(["", CT.octet, CT.plain]);

/** A well-formed `type/subtype`, per the RFC 2045 token grammar. */
const MEDIA_TYPE_RE = /^[a-z0-9][a-z0-9!#$&^_.+-]*\/[a-z0-9][a-z0-9!#$&^_.+-]*$/;

/**
 * Reduces a declared media type to its canonical, parameter-free, lowercase
 * form. Returns "" when the input is not a well-formed media type.
 */
export function normalizeContentType(declared: string | undefined | null): string {
  if (!declared) return "";
  const base = (declared.split(";")[0] ?? "").trim().toLowerCase();
  if (!MEDIA_TYPE_RE.test(base)) return "";
  return ALIASES[base] ?? base;
}

/** True for types whose renderer executes author-supplied script or markup. */
export function isActiveType(ct: string): boolean {
  return ACTIVE.has(normalizeContentType(ct));
}

/** True for declarations that carry no information about the content's shape. */
export function isGenericType(ct: string): boolean {
  return GENERIC.has(normalizeContentType(ct));
}

/** True for types that hold human-readable text. */
export function isTextualType(ct: string): boolean {
  const n = normalizeContentType(ct);
  if (n.startsWith("text/")) return true;
  const structured: string[] = [CT.json, CT.ndjson, CT.xml, CT.yaml, CT.svg, CT.javascript, CT.sql];
  return structured.includes(n);
}

/** Extension-to-type map for the filename fallback. */
const BY_EXTENSION: Record<string, string> = {
  json: CT.json,
  ndjson: CT.ndjson,
  jsonl: CT.ndjson,
  csv: CT.csv,
  tsv: CT.tsv,
  xml: CT.xml,
  yaml: CT.yaml,
  yml: CT.yaml,
  md: CT.markdown,
  markdown: CT.markdown,
  txt: CT.plain,
  log: CT.plain,
  sql: CT.sql,
  py: CT.python,
  js: CT.javascript,
  mjs: CT.javascript,
  html: CT.html,
  htm: CT.html,
  jsx: CT.jsx,
  svg: CT.svg,
  pdf: CT.pdf,
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  bmp: "image/bmp",
  ico: "image/x-icon",
  mp3: "audio/mpeg",
  wav: "audio/wav",
  ogg: "audio/ogg",
  oga: "audio/ogg",
  m4a: "audio/mp4",
  flac: "audio/flac",
  mp4: "video/mp4",
  webm: "video/webm",
  ogv: "video/ogg",
  mov: "video/quicktime",
  zip: "application/zip",
  gz: "application/gzip",
};

/** Maps a filename's extension to a media type, or "" when unrecognized. */
export function typeFromFileName(fileName: string | undefined): string {
  if (!fileName) return "";
  const dot = fileName.lastIndexOf(".");
  if (dot < 0 || dot === fileName.length - 1) return "";
  return BY_EXTENSION[fileName.slice(dot + 1).toLowerCase()] ?? "";
}

/** How much of the content the structured-text heuristics examine. */
const SNIFF_LEN = 8192;

/**
 * Detects the media type of textual content. Only ever returns a passive
 * structured-text family or "": it cannot promote content to an active type,
 * and it has no way to inspect binary families (the viewer only holds text).
 */
export function detectTextType(content: string): string {
  const truncated = content.length > SNIFF_LEN;
  const prefix = truncated ? content.slice(0, SNIFF_LEN) : content;
  // \s covers the UTF-8 BOM in JavaScript regular expressions.
  const body = prefix.replace(/^\s+/, "");
  if (!body) return "";

  if (looksLikeNdjson(body, truncated)) return CT.ndjson;
  if (looksLikeJson(body, truncated)) return CT.json;
  if (looksLikeXml(body)) return CT.xml;
  if (looksLikeYaml(body)) return CT.yaml;
  if (looksDelimited(body, ",", truncated)) return CT.csv;
  if (looksDelimited(body, "\t", truncated)) return CT.tsv;
  return "";
}

/**
 * Resolves the type to render content under: a specific declaration wins,
 * otherwise the filename extension, otherwise the content itself. Falls back to
 * the normalized declaration so nothing is ever rendered as "".
 */
export function resolveContentType(
  declared: string,
  fileName?: string,
  content?: string,
): string {
  const norm = normalizeContentType(declared);
  if (norm && !isGenericType(norm)) return norm;

  const byName = typeFromFileName(fileName);
  // The filename is author-controlled, so it is held to the same rule as
  // content sniffing: it may not promote a generic declaration to an active
  // type. A ".html" name on a text/plain asset stays text.
  if (byName && !isActiveType(byName)) return byName;

  if (content !== undefined) {
    const sniffed = detectTextType(content);
    if (sniffed) return sniffed;
  }
  return norm || CT.octet;
}

// --- structured-text heuristics -------------------------------------------

/** Lines known to be whole, dropping the fragment the sniff window cut off. */
function completeLines(body: string, truncated: boolean): string[] {
  const idx = body.lastIndexOf("\n");
  const whole = idx < 0 ? (truncated ? "" : body) : body.slice(0, idx);
  return whole
    .split("\n")
    .map((l) => l.replace(/\r$/, ""))
    .filter((l) => l.trim().length > 0)
    .slice(0, 64);
}

function looksLikeJson(body: string, truncated: boolean): boolean {
  if (body[0] !== "{" && body[0] !== "[") return false;
  if (!truncated) {
    try {
      JSON.parse(body);
      return true;
    } catch {
      return false;
    }
  }
  // A window-truncated document cannot be parsed whole. Balanced-delimiter
  // scanning over the prefix is enough evidence: nothing but JSON opens with a
  // brace and keeps its strings and structure well-formed for 8 KB.
  return scanJsonPrefix(body);
}

/**
 * Characters that may appear outside a string in a well-formed JSON document:
 * whitespace, the value separators, everything a number is made of, and the
 * letters of `true`, `false` and `null`.
 */
const JSON_BARE_CHARS = /[\s,:+\-.eE0-9truflsan]/;

/** Where the scanner is inside a JSON string literal. */
interface StringState {
  inString: boolean;
  escaped: boolean;
}

/** Advances the string state by one character while inside a string literal. */
function stepInString(state: StringState, ch: string): void {
  if (state.escaped) state.escaped = false;
  else if (ch === "\\") state.escaped = true;
  else if (ch === '"') state.inString = false;
}

/**
 * Walks a JSON prefix, tracking string state and nesting. Returns false on any
 * character that could not appear in a well-formed document at that position.
 */
function scanJsonPrefix(body: string): boolean {
  const state: StringState = { inString: false, escaped: false };
  let depth = 0;

  for (const ch of body) {
    if (state.inString) {
      stepInString(state, ch);
      continue;
    }
    const delta = depthDelta(ch);
    if (delta === null) return false;
    depth += delta;
    if (depth < 0) return false;
    if (ch === '"') state.inString = true;
  }
  return depth > 0 || !state.inString;
}

/**
 * The nesting change a character outside a string makes: +1 for an opening
 * delimiter, -1 for a closing one, 0 for anything else that may legally appear
 * there, and null for a character that could not.
 */
function depthDelta(ch: string): number | null {
  if (ch === "{" || ch === "[") return 1;
  if (ch === "}" || ch === "]") return -1;
  if (ch === '"' || JSON_BARE_CHARS.test(ch)) return 0;
  return null;
}

function looksLikeNdjson(body: string, truncated: boolean): boolean {
  if (body[0] !== "{" && body[0] !== "[") return false;
  const lines = completeLines(body, truncated);
  if (lines.length < 2) return false;
  return lines.every((line) => {
    const t = line.trim();
    if (t[0] !== "{" && t[0] !== "[") return false;
    try {
      JSON.parse(t);
      return true;
    } catch {
      return false;
    }
  });
}

/**
 * Root element names detection refuses to classify as XML. These open the
 * active families, and routing them to the XML viewer would classify
 * script-bearing markup into a family detection may not reach.
 */
const ACTIVE_ROOT_TAGS = new Set([
  "html", "head", "body", "div", "span", "p", "a", "img", "table", "script",
  "style", "ul", "ol", "li", "h1", "form", "input", "button", "section", "main", "svg",
]);

function looksLikeXml(body: string): boolean {
  if (body.startsWith("<?xml")) return true;
  if (body[0] !== "<" || body.length < 3) return false;
  if (body[1] === "!" || body[1] === "?" || body[1] === "/") return false;
  const match = /^<([A-Za-z_:][\w.:-]*)[\s/>]/.exec(body);
  if (!match) return false;
  return !ACTIVE_ROOT_TAGS.has((match[1] ?? "").toLowerCase());
}

function looksLikeYaml(body: string): boolean {
  if (body.startsWith("%YAML")) return true;
  if (!body.startsWith("---")) return false;
  const rest = body[3];
  return rest === undefined || rest === "\n" || rest === "\r" || rest === " " || rest === "\t";
}

const MIN_DELIMITED_ROWS = 3;
const MIN_DELIMITED_COLUMNS = 2;

/**
 * True when the content parses as a delimiter-separated table: at least three
 * whole lines, each with the same field count, and at least two fields. Prose
 * with incidental commas does not hold a consistent field count for three lines.
 */
function looksDelimited(body: string, delim: string, truncated: boolean): boolean {
  const lines = completeLines(body, truncated);
  if (lines.length < MIN_DELIMITED_ROWS) return false;
  if (!(lines[0] ?? "").includes(delim)) return false;

  let expected = -1;
  for (const line of lines) {
    const count = countFields(line, delim);
    if (count < MIN_DELIMITED_COLUMNS) return false;
    if (expected < 0) expected = count;
    else if (count !== expected) return false;
  }
  return true;
}

/** Counts delimiter-separated fields, honoring double-quoted sections. */
function countFields(line: string, delim: string): number {
  let fields = 1;
  let inQuotes = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (ch === '"') {
      if (inQuotes && line[i + 1] === '"') i++;
      else inQuotes = !inQuotes;
    } else if (ch === delim && !inQuotes) {
      fields++;
    }
  }
  return fields;
}
