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
  /** A label to show before (or instead of) a server-resolved name. */
  fallbackLabel: string;
}

/** ResolvedRef is the server's resolution of a reference URN to a display label. */
export interface ResolvedRef {
  urn: string;
  type: string;
  label: string;
  exists: boolean;
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
    return { urn: trimmed, type: "datahub", fallbackLabel: datahubLabel(trimmed) };
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
      return { urn: trimmed, type, fallbackLabel: id };
    case "connection": {
      const m = id.match(/^\(([^,]+),([^)]+)\)$/);
      if (!m) return null;
      return { urn: trimmed, type: "connection", fallbackLabel: `${m[2]} (${m[1]})` as string };
    }
    default:
      return null;
  }
}
