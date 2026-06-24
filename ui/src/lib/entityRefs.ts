// Client-side helpers for the inline entity references that knowledge-page
// bodies carry (#664). The serialized forms mirror the Go projection: mcp:<type>:<id>
// for internal entities (mcp:connection:(kind,name) for connections) and a urn:
// URN for DataHub. These helpers extract the references from a body and derive a
// type + fallback label for rendering before the server resolve completes.

export type RefType =
  | "asset"
  | "prompt"
  | "collection"
  | "knowledge_page"
  | "connection"
  | "datahub"
  | "unknown";

export interface ParsedRef {
  urn: string;
  type: RefType;
  /** The raw id for single-id internal types (asset/prompt/collection/page); "" otherwise. */
  id: string;
  /** A label to show before (or instead of) a server-resolved name. */
  fallbackLabel: string;
}

// SAFE_ID matches the server's mcp: simple-id charset. A reference id that
// contains anything else (for example a crafted `../../admin` path-traversal in a
// markdown link href) is treated as non-navigable rather than interpolated into a
// route.
const SAFE_ID = /^[A-Za-z0-9_.-]+$/;

/** entityHref returns the in-app path to an entity, or null if it has no route. */
export function entityHref(type: string, id: string): string | null {
  if (!id || !SAFE_ID.test(id)) return null;
  switch (type) {
    case "asset":
      return `/assets/${id}`;
    case "collection":
      return `/collections/${id}`;
    case "prompt":
      return `/prompts/${id}`;
    default:
      return null; // knowledge_page (no URL route), connection, datahub
  }
}

/** ResolvedRef is the server's resolution of a reference URN to a display label. */
export interface ResolvedRef {
  urn: string;
  type: string;
  label: string;
  exists: boolean;
  /** False when the viewer may not access the target; such refs are not shown. */
  accessible: boolean;
}

// Mirrors the backend refTokenRe: at most one level of parentheses, which covers
// every reference form. Parenthesized alternatives come first so a connection or
// DataHub token is matched whole rather than truncated at an enclosing paren.
const REF_TOKEN_RE =
  /mcp:[a-z_]+:\([^)]*\)|mcp:[a-z_]+:[A-Za-z0-9_.-]+|urn:[a-z]+:[A-Za-z0-9]+:\([^)]*\)|urn:[a-z]+:[^\s)\]>]+/g;

// Fenced code blocks and inline code spans are stripped before scanning so a URN
// shown as a documentation example is not treated as a reference (mirrors the
// server's codeSpanRe).
const CODE_SPAN_RE = /```[\s\S]*?```|`[^`]*`/g;

/** PickableRefType is an entity type the manual-reference picker can search. */
export type PickableRefType = "asset" | "collection" | "knowledge_page" | "prompt";

/** buildRefUrn serializes an internal entity reference for a single-id type. */
export function buildRefUrn(type: PickableRefType, id: string): string {
  return `mcp:${type}:${id}`;
}

/** isRefUrn reports whether an href is a serialized entity reference. */
export function isRefUrn(href: string | undefined): boolean {
  if (!href) return false;
  return href.startsWith("mcp:") || href.startsWith("urn:");
}

/** extractRefUrns returns the distinct reference URNs mentioned in a body. */
export function extractRefUrns(body: string): string[] {
  const matches = body.replace(CODE_SPAN_RE, " ").match(REF_TOKEN_RE) ?? [];
  return Array.from(new Set(matches.filter((m) => parseRef(m) !== null)));
}

/** datahubLabel pulls a readable name out of a DataHub URN. */
function datahubLabel(urn: string): string {
  const prefix = "urn:li:dataset:(";
  if (urn.startsWith(prefix)) {
    const inner = urn.slice(prefix.length).replace(/\)$/, "");
    const name = inner.split(",")[1];
    if (name) return name;
  }
  const i = urn.lastIndexOf(":");
  return i >= 0 && i < urn.length - 1 ? urn.slice(i + 1) : urn;
}

/** parseRef parses a serialized reference into its type and a fallback label. */
export function parseRef(urn: string): ParsedRef | null {
  const trimmed = urn.trim();
  if (trimmed.startsWith("urn:")) {
    return { urn: trimmed, type: "datahub", id: "", fallbackLabel: datahubLabel(trimmed) };
  }
  if (!trimmed.startsWith("mcp:")) return null;

  const rest = trimmed.slice("mcp:".length);
  const sep = rest.indexOf(":");
  if (sep < 0) return null;
  const type = rest.slice(0, sep);
  const id = rest.slice(sep + 1);
  if (!id) return null;

  switch (type) {
    case "asset":
    case "prompt":
    case "collection":
    case "knowledge_page":
      return { urn: trimmed, type, id, fallbackLabel: id };
    case "connection": {
      const m = id.match(/^\(([^,]+),([^)]+)\)$/);
      if (!m) return null;
      return { urn: trimmed, type: "connection", id: "", fallbackLabel: `${m[2]} (${m[1]})` as string };
    }
    default:
      return null;
  }
}
