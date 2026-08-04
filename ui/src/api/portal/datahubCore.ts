// Shared vocabulary for the portal DataHub API modules (#718): the response
// types, the request-threshold constant, and the two URL helpers every module
// builds its paths with. It exists because the surface outgrew one module
// (#1158) and the parts have to agree on these; everything here is re-exported
// from ./datahub, which stays the one import path for callers.

// MIN_SEARCH_LEN is the shortest query that triggers a search request. The tabs
// only render search results at this length, so the query hooks stay disabled
// below it to avoid a wasted request per leading keystroke.
export const MIN_SEARCH_LEN = 2;

// enc and base build a connection-scoped path. A connection name is
// user-supplied, so it is encoded rather than interpolated raw.
export const enc = encodeURIComponent;
export const base = (conn: string) => `/datahub/${enc(conn)}`;

// catalogKey is the query-key prefix every catalog read shares. Governance
// writes invalidate at this prefix because a vocabulary change reaches the
// lists, the pickers, and each entity's chips alike.
export const catalogKey = (conn: string) => ["datahub", conn, "catalog"] as const;

// --- types (mirror pkg/semantic and pkg/portal/datahubapi JSON) ---

export interface DataHubConnection {
  name: string;
  writable: boolean;
}

export interface TableSearchResult {
  urn: string;
  name: string;
  platform?: string;
  description?: string;
  tags?: string[];
  domain?: string;
  matched_field?: string;
}

export interface Owner {
  urn: string;
  type: string;
  name?: string;
  email?: string;
}

export interface GlossaryTerm {
  urn: string;
  name: string;
  description?: string;
}

export interface Domain {
  urn: string;
  name: string;
  description?: string;
}

// GlossaryNode is a directory in the business glossary (#1155). terms_count and
// nodes_count are DataHub's own tally of the direct children, so a branch can be
// rendered as expandable without first fetching what is inside it.
export interface GlossaryNode {
  urn: string;
  name: string;
  description?: string;
  parent_node?: string;
  terms_count: number;
  nodes_count: number;
}

// GlossaryRoots is the top of the glossary. Nodes and terms carry separate
// totals because DataHub pages the two independently.
export interface GlossaryRoots {
  nodes: GlossaryNode[];
  nodes_total: number;
  terms: GlossaryTerm[];
  terms_total: number;
}

// GlossaryChildren is one page of what sits directly under a node. start, count,
// and total describe the combined page: DataHub pages a node's nodes and terms
// as one mixed collection.
export interface GlossaryChildren {
  nodes: GlossaryNode[];
  terms: GlossaryTerm[];
  start: number;
  count: number;
  total: number;
}

export interface Deprecation {
  deprecated: boolean;
  note?: string;
  actor?: string;
  decommission_date?: string;
}

export interface TableContext {
  urn?: string;
  description?: string;
  owners?: Owner[];
  tags?: string[];
  // tag_refs mirrors tags as URN + name pairs so the editor removes/dedupes a tag
  // by its URN (tags carries only the display name). Populated on the detail read.
  tag_refs?: EntityRef[];
  glossary_terms?: GlossaryTerm[];
  domain?: Domain | null;
  deprecation?: Deprecation | null;
  quality_score?: number | null;
  custom_properties?: Record<string, string>;
  last_modified?: string | null;
}

export interface ColumnContext {
  name: string;
  description?: string;
  tags?: string[];
  glossary_terms?: GlossaryTerm[];
  is_pii?: boolean;
  is_sensitive?: boolean;
  business_name?: string;
}

export interface CatalogEntity {
  urn: string;
  context: TableContext | null;
  columns?: Record<string, ColumnContext>;
}

export interface ContextDocument {
  urn: string;
  title: string;
  sub_type?: string;
  snippet?: string;
  body?: string;
  status?: string;
  show_in_global_context: boolean;
  related_asset_urns?: string[];
}

export interface OwnerChange {
  owner_urn: string;
  ownership_type?: string;
}

// EntityRef is a URN + display-name result from a catalog metadata picker lookup
// (#785): the user picks by name, the UI submits the urn.
export interface EntityRef {
  urn: string;
  name: string;
  description?: string;
}

// documentId extracts the bare id from a context-document URN
// (urn:li:document:<id> -> <id>) for the update/delete paths.
export function documentId(urn: string): string {
  return urn.replace(/^urn:li:document:/, "");
}

