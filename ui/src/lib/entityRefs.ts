// Client-side helpers for the inline entity references that knowledge-page
// bodies carry (#664). The serialized forms mirror the Go projection:
// mcp:<type>:<id> for internal entities, mcp:connection:(kind,name) for a
// connection, and the platform catalog's mcp:dataset:(kind,connection,path)
// family. These helpers extract the references from a body and derive a type +
// fallback label for rendering before the server resolve completes.

export type RefType =
  | "asset"
  | "prompt"
  | "collection"
  | "knowledge_page"
  | "connection"
  | "script"
  | "catalog"
  | "unknown";

export interface ParsedRef {
  urn: string;
  type: RefType;
  /** The raw id for single-id internal types (asset/prompt/collection/page/script); "" otherwise. */
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
    case "script":
      // A cited managed script (#1855) opens its script page, which every
      // signed-in reader may read (#2027).
      return `/automations/${id}`;
    default:
      // Connections have no per-instance portal page. A catalog reference does,
      // but it is keyed by the whole reference rather than a simple id, so it is
      // built by catalogHref instead.
      return null;
  }
}

/**
 * CatalogSubTab is a surface inside the portal's Catalog section (#1194). It is
 * declared here, with the rest of the reference route table, because which inner
 * tab a catalog reference opens is part of where that reference points.
 */
export type CatalogSubTab = "tables" | "glossary" | "tags" | "domains" | "data_products";

/**
 * CATALOG_SUB_HASHES spells each inner tab for a URL. The type members are
 * identifiers, so data_products cannot carry the hyphen the URL wants.
 */
export const CATALOG_SUB_HASHES: Record<CatalogSubTab, string> = {
  tables: "tables",
  glossary: "glossary",
  tags: "tags",
  domains: "domains",
  data_products: "data-products",
};

// CATALOG_SUB_BY_KIND maps a catalog reference's kind to the Catalog inner tab
// that shows it (#1159). A kind that is not here (a dataset, a column, a schema
// document) belongs to Tables, which is the default.
const CATALOG_SUB_BY_KIND: Record<string, CatalogSubTab> = {
  glossary_term: "glossary",
  glossary_node: "glossary",
  tag: "tags",
  domain: "domains",
  data_product: "data_products",
};

/** CATALOG_KINDS are the kinds of reference the platform catalog holds. */
const CATALOG_KINDS = new Set([
  "dataset",
  "column",
  "glossary_term",
  "glossary_node",
  "tag",
  "domain",
  "data_product",
  "schema_document",
]);

/** catalogKind returns the kind segment of a catalog reference
 * ("mcp:tag:pii" -> "tag"), or "" when the string is not one. */
export function catalogKind(ref: string): string {
  const trimmed = ref.trim();
  if (!trimmed.startsWith("mcp:")) return "";
  const rest = trimmed.slice("mcp:".length);
  const sep = rest.indexOf(":");
  const kind = sep > 0 ? rest.slice(0, sep) : "";
  return CATALOG_KINDS.has(kind) ? kind : "";
}

/** isCatalogRef reports whether a string is a platform catalog reference. */
export function isCatalogRef(ref: string): boolean {
  return catalogKind(ref) !== "";
}

/**
 * catalogSubForRef returns the Catalog inner tab that shows a catalog entity.
 * A definition (a glossary term or node, a tag, a domain, a data product) has
 * its own tab, so a reference to one opens where it is managed rather than on
 * Tables, which cannot show it.
 */
export function catalogSubForRef(ref: string): CatalogSubTab {
  return CATALOG_SUB_BY_KIND[catalogKind(ref)] ?? "tables";
}

/** CATALOG_REF_PARAM is the query parameter that deep-links one catalog entity. */
export const CATALOG_REF_PARAM = "ref";

/**
 * catalogHref returns the in-app path that opens one catalog entity in the
 * Knowledge > Catalog section. A catalog reference is keyed by the whole
 * reference, not by a simple id, so it cannot go through entityHref's id-based
 * route table. A column opens its dataset.
 *
 * The inner tab rides in the hash rather than the path (#1194): Catalog is one
 * route, and a second route would remount the hub.
 *
 * Returns null for anything that is not a catalog reference, so a malformed
 * reference is treated as non-navigable rather than interpolated into a link.
 */
export function catalogHref(ref: string): string | null {
  const trimmed = ref.trim();
  const kind = catalogKind(trimmed);
  if (!kind) return null;
  const target = kind === "column" ? columnDataset(trimmed) : trimmed;
  const sub = catalogSubForRef(trimmed);
  return `/knowledge/catalog?${CATALOG_REF_PARAM}=${encodeURIComponent(target)}#${CATALOG_SUB_HASHES[sub]}`;
}

/** columnDataset is the dataset reference a column reference belongs to:
 * mcp:column:(trino,acme,a.b.c,amount) -> mcp:dataset:(trino,acme,a.b.c). */
export function columnDataset(ref: string): string {
  const m = ref.trim().match(/^mcp:column:\((.*),("(?:[^"]|"")*"|[^,"]*)\)$/);
  return m ? `mcp:dataset:(${m[1]})` : ref;
}

// REF_SPECIAL are the characters a reference part is quoted for, as the
// server's quotePart has them.
const REF_SPECIAL = /[.,()"]/;

/** quoteRefPart renders one part of a reference the way the server does: bare
 * when it can be, double-quoted with inner quotes doubled otherwise. */
export function quoteRefPart(p: string): string {
  if (p !== "" && !REF_SPECIAL.test(p) && p.trim() === p) return p;
  return `"${p.replace(/"/g, '""')}"`;
}

/** columnRef is the reference of one column of a dataset:
 * mcp:dataset:(trino,acme,a.b.c) + amount -> mcp:column:(trino,acme,a.b.c,amount). */
export function columnRef(datasetRef: string, column: string): string {
  const inner = datasetRef.trim().replace(/^mcp:dataset:\(/, "").replace(/\)$/, "");
  return `mcp:column:(${inner},${quoteRefPart(column)})`;
}

/** refHref returns the destination for any parsed reference: the id-based route
 * for internal entities, the catalog deep link for a catalog reference. One
 * door, so every surface that renders a reference navigates the same way. */
export function refHref(type: string, id: string, urn: string): string | null {
  return type === "catalog" ? catalogHref(urn) : entityHref(type, id);
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
// every reference form. The parenthesized alternative comes first so a
// connection or dataset token is matched whole rather than truncated at an
// enclosing paren.
// Exported as a source string so other modules (the markdown renderer) build a
// fresh RegExp from the same pattern rather than copying it.
export const REF_TOKEN_SOURCE =
  "mcp:[a-z_]+:\\([^)]*\\)|mcp:[a-z_]+:[A-Za-z0-9_.-]+";
const REF_TOKEN_RE = new RegExp(REF_TOKEN_SOURCE, "g");

// Fenced code blocks and inline code spans are stripped before scanning so a
// reference shown as a documentation example is not treated as a reference (mirrors the
// server's codeSpanRe).
const CODE_SPAN_RE = /```[\s\S]*?```|`[^`]*`/g;

// TRAILING_PUNCT_RE mirrors the server's trailingPunct trim (entity_ref_scan.go,
// #704). An undelimited mcp: token written in prose immediately before
// sentence punctuation absorbs that punctuation into the match (the mcp id class
// includes "."), so it is trimmed before the token is parsed or resolved. The
// parenthesized forms already terminate at ")", so this never touches
// punctuation inside a token. Keeping this in lockstep with the server is what
// prevents the rendered chip and the stored reference from diverging.
const TRAILING_PUNCT_RE = /[.,;:!?]+$/;

/** trimRefToken strips a trailing run of sentence punctuation from a scanned token. */
export function trimRefToken(token: string): string {
  return token.replace(TRAILING_PUNCT_RE, "");
}

/** PickableRefType is an entity type the manual-reference picker can search: the
 * portal's own single-id entities, plus the catalog definitions a page can be
 * written about (#1159). */
export type PickableRefType =
  | "asset"
  | "collection"
  | "knowledge_page"
  | "prompt"
  | "glossary_term"
  | "tag"
  | "domain";

/** CATALOG_REF_TYPES are the pickable types the platform catalog holds. They
 * are picked from the catalog, which returns their reference, so they never go
 * through buildRefUrn. */
const CATALOG_REF_TYPES = new Set<PickableRefType>(["glossary_term", "tag", "domain"]);

/** isCatalogRefType reports whether a pickable type lives in the platform
 * catalog rather than among the portal's own entities. */
export function isCatalogRefType(type: PickableRefType): boolean {
  return CATALOG_REF_TYPES.has(type);
}

/** buildRefUrn serializes an internal entity reference for a single-id type. */
export function buildRefUrn(type: PickableRefType, id: string): string {
  return `mcp:${type}:${id}`;
}

/** isRefUrn reports whether an href is a serialized entity reference. */
export function isRefUrn(href: string | undefined): boolean {
  if (!href) return false;
  return href.startsWith("mcp:");
}

/** extractRefUrns returns the distinct reference URNs mentioned in a body. */
export function extractRefUrns(body: string): string[] {
  const matches = body.replace(CODE_SPAN_RE, " ").match(REF_TOKEN_RE) ?? [];
  return Array.from(
    new Set(matches.map(trimRefToken).filter((m) => parseRef(m) !== null)),
  );
}

/** parseRef parses a serialized reference into its type and a fallback label. */
export function parseRef(urn: string): ParsedRef | null {
  const trimmed = urn.trim();
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
    case "script":
      return { urn: trimmed, type, id, fallbackLabel: id };
    case "connection": {
      const m = id.match(/^\(([^,]+),([^)]+)\)$/);
      if (!m) return null;
      return { urn: trimmed, type: "connection", id: "", fallbackLabel: `${m[2]} (${m[1]})` as string };
    }
    case "dataset":
    case "column":
    case "glossary_term":
    case "glossary_node":
    case "tag":
    case "domain":
    case "data_product":
    case "schema_document":
      return { urn: trimmed, type: "catalog", id: "", fallbackLabel: platformCatalogLabel(type, id) };
    default:
      return null;
  }
}

/**
 * platformCatalogLabel names a platform catalog reference before the server
 * resolves it: the table (and column) a dataset or column reference
 * names, the connection and filename of a schema document (#1918), the key a
 * definition reference carries.
 */
function platformCatalogLabel(type: string, key: string): string {
  if (type === "schema_document") return schemaDocumentLabel(key);
  if (type !== "dataset" && type !== "column") return key;
  const parts = key.replace(/^\(|\)$/g, "").split(",");
  const path = (parts[2] ?? "").split(".");
  const table = path[path.length - 1] || key;
  return type === "column" && parts[3] ? `${table}.${parts[3]}` : table;
}

/** schemaDocumentLabel is a schema document reference's connection and
 * filename: (api,billing,"openapi.json") reads "billing openapi.json". */
function schemaDocumentLabel(key: string): string {
  const parts = key.replace(/^\(|\)$/g, "").split(",");
  return `${parts[1] ?? ""} ${(parts[2] ?? "").replace(/^"|"$/g, "")}`.trim() || key;
}
