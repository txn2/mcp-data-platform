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
    case "knowledge_page":
      // Knowledge pages are URL-addressable (#709) so references deep-link, the
      // browser back/forward works, and the reference graph is wiki-navigable.
      return `/knowledge/pages/${id}`;
    default:
      // Connections have no per-instance portal page. A DataHub URN does, but it
      // is keyed by the whole URN rather than a simple id, so it is built by
      // catalogHref instead.
      return null;
  }
}

/**
 * CatalogSubTab is a surface inside the portal's Catalog section (#1194). It is
 * declared here, with the rest of the reference route table, because which inner
 * tab a catalog reference opens is part of where that reference points.
 */
export type CatalogSubTab = "tables" | "context_docs" | "tags" | "domains" | "glossary";

/**
 * CATALOG_SUB_HASHES spells each inner tab for a URL. The type members are
 * identifiers, so context_docs cannot carry the hyphen the URL wants.
 */
export const CATALOG_SUB_HASHES: Record<CatalogSubTab, string> = {
  tables: "tables",
  context_docs: "context-docs",
  tags: "tags",
  domains: "domains",
  glossary: "glossary",
};

// CATALOG_SUB_BY_URN_TYPE maps a DataHub entity type to the Catalog inner tab
// that shows that kind of entity (#1159). A type that is not here — a dataset,
// most of all — belongs to Tables, which is the default.
const CATALOG_SUB_BY_URN_TYPE: Record<string, CatalogSubTab> = {
  glossaryTerm: "glossary",
  glossaryNode: "glossary",
  tag: "tags",
  domain: "domains",
};

/** datahubEntityType extracts the entity-type segment of a DataHub URN
 * ("urn:li:tag:pii" -> "tag"), or "" when the URN is not of that form. */
function datahubEntityType(urn: string): string {
  if (!urn.startsWith("urn:li:")) return "";
  const rest = urn.slice("urn:li:".length);
  const sep = rest.indexOf(":");
  return sep > 0 ? rest.slice(0, sep) : "";
}

/**
 * catalogSubForURN returns the Catalog inner tab that shows a catalog entity.
 * A governance entity — a glossary term or node, a tag, a domain — has its own
 * tab under Catalog, so a reference to one opens where it is managed rather
 * than on Tables, which cannot show it at all.
 */
export function catalogSubForURN(urn: string): CatalogSubTab {
  return CATALOG_SUB_BY_URN_TYPE[datahubEntityType(urn.trim())] ?? "tables";
}

/**
 * catalogHref returns the in-app path that opens one catalog entity in the
 * Knowledge > Catalog tab. A DataHub reference is keyed by its whole URN, not by
 * a simple id, so it cannot go through entityHref's id-based route table.
 *
 * The inner tab rides in the hash rather than the path (#1194): Catalog is one
 * route, and a second route would remount the hub and drop the connection its
 * inner tabs share.
 *
 * Returns null for anything that is not a URN, so a malformed reference is
 * treated as non-navigable rather than interpolated into a link.
 */
export function catalogHref(urn: string): string | null {
  const trimmed = urn.trim();
  if (!trimmed.startsWith(externalURNPrefix)) return null;
  const sub = catalogSubForURN(trimmed);
  return `/knowledge/catalog?urn=${encodeURIComponent(trimmed)}#${CATALOG_SUB_HASHES[sub]}`;
}

/** externalURNPrefix marks a catalog (DataHub) reference. */
const externalURNPrefix = "urn:";

/** refHref returns the destination for any parsed reference: the id-based route
 * for internal entities, the catalog deep link for a DataHub URN. One door, so
 * every surface that renders a reference navigates the same way. */
export function refHref(type: string, id: string, urn: string): string | null {
  return type === "datahub" ? catalogHref(urn) : entityHref(type, id);
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
// Exported as a source string so other modules (the markdown renderer) build a
// fresh RegExp from the same pattern rather than copying it.
export const REF_TOKEN_SOURCE =
  "mcp:[a-z_]+:\\([^)]*\\)|mcp:[a-z_]+:[A-Za-z0-9_.-]+|urn:[a-z]+:[A-Za-z0-9]+:\\([^)]*\\)|urn:[a-z]+:[^\\s)\\]>]+";
const REF_TOKEN_RE = new RegExp(REF_TOKEN_SOURCE, "g");

// Fenced code blocks and inline code spans are stripped before scanning so a URN
// shown as a documentation example is not treated as a reference (mirrors the
// server's codeSpanRe).
const CODE_SPAN_RE = /```[\s\S]*?```|`[^`]*`/g;

// TRAILING_PUNCT_RE mirrors the server's trailingPunct trim (entity_ref_scan.go,
// #704). An undelimited mcp:/urn: token written in prose immediately before
// sentence punctuation absorbs that punctuation into the match (the mcp id class
// includes "." and the bare-urn class stops only at whitespace or a closing
// bracket), so it is trimmed before the token is parsed or resolved. The
// parenthesized forms already terminate at ")", so this never touches
// punctuation inside a token. Keeping this in lockstep with the server is what
// prevents the rendered chip and the stored reference from diverging.
const TRAILING_PUNCT_RE = /[.,;:!?]+$/;

/** trimRefToken strips a trailing run of sentence punctuation from a scanned token. */
export function trimRefToken(token: string): string {
  return token.replace(TRAILING_PUNCT_RE, "");
}

/** PickableRefType is an entity type the manual-reference picker can search: the
 * portal's own single-id entities, plus the DataHub governance vocabularies a
 * page can be written about (#1159). */
export type PickableRefType =
  | "asset"
  | "collection"
  | "knowledge_page"
  | "prompt"
  | "glossary_term"
  | "tag"
  | "domain";

/** CATALOG_REF_TYPES are the pickable types the DataHub catalog holds. They are
 * picked and stored as catalog URNs, not as mcp: references, so they need a
 * connection to search and never go through buildRefUrn. */
const CATALOG_REF_TYPES = new Set<PickableRefType>(["glossary_term", "tag", "domain"]);

/** isCatalogRefType reports whether a pickable type lives in the DataHub
 * catalog rather than the portal's own database. */
export function isCatalogRefType(type: PickableRefType): boolean {
  return CATALOG_REF_TYPES.has(type);
}

/** buildRefUrn serializes an internal entity reference for a single-id type. A
 * catalog type has no id-based form, so it is not one of these: its URN comes
 * from the catalog itself. */
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
  return Array.from(
    new Set(matches.map(trimRefToken).filter((m) => parseRef(m) !== null)),
  );
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
