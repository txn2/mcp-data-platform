// DataHub Catalog and Context Docs API (#719/#720). Thin typed hooks over the
// portal DataHub REST surface (#718) at /api/v1/portal/datahub/{connection}/...
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "./client";

// MIN_SEARCH_LEN is the shortest query that triggers a search request. The tabs
// only render search results at this length, so the query hooks stay disabled
// below it to avoid a wasted request per leading keystroke.
export const MIN_SEARCH_LEN = 2;

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

// --- query keys ---

const keys = {
  connections: ["datahub", "connections"] as const,
  catalogBrowse: (conn: string, limit: number, offset: number) =>
    ["datahub", conn, "catalog", "browse", limit, offset] as const,
  catalogSearch: (conn: string, q: string, limit: number) =>
    ["datahub", conn, "catalog", "search", q, limit] as const,
  entity: (conn: string, urn: string) => ["datahub", conn, "catalog", "entity", urn] as const,
  lookupTags: (conn: string, q: string) => ["datahub", conn, "catalog", "lookup", "tags", q] as const,
  lookupGlossary: (conn: string, q: string) =>
    ["datahub", conn, "catalog", "lookup", "glossary", q] as const,
  lookupDomains: (conn: string) => ["datahub", conn, "catalog", "lookup", "domains"] as const,
  docsBrowse: (conn: string, limit: number, offset: number) =>
    ["datahub", conn, "documents", "browse", limit, offset] as const,
  docsSearch: (conn: string, q: string, limit: number) =>
    ["datahub", conn, "documents", "search", q, limit] as const,
  doc: (conn: string, id: string) => ["datahub", conn, "documents", id] as const,
};

const enc = encodeURIComponent;
const base = (conn: string) => `/datahub/${enc(conn)}`;

// --- connections ---

export function useDataHubConnections() {
  return useQuery({
    queryKey: keys.connections,
    queryFn: () =>
      apiFetch<{ connections: DataHubConnection[] }>("/datahub/connections").then(
        (r) => r.connections ?? [],
      ),
  });
}

// --- catalog reads ---

// Catalog and context-document browse are deliberately NOT wired to offset-based
// infinite scroll (unlike the internal portal lists in #972). They page against
// an external DataHub via a `query:"*"` search that sends no sort criteria, so
// results come back in relevance/segment order that is not stable across
// requests: numeric start/count offset paging over it can drop or duplicate rows
// between page fetches. Catalog browse also returns no total. Wiring infinite
// scroll here would introduce a correctness bug worse than the current
// first-page cap; it stays gated on upstream mcp-datahub gaining a stable sort
// (or a scroll/search-after cursor). Search remains the escape hatch for now.
export function useCatalogBrowse(conn: string, opts: { limit?: number; offset?: number } = {}) {
  const limit = opts.limit ?? 50;
  const offset = opts.offset ?? 0;
  return useQuery({
    queryKey: keys.catalogBrowse(conn, limit, offset),
    enabled: !!conn,
    queryFn: () =>
      apiFetch<{ results: TableSearchResult[] }>(
        `${base(conn)}/catalog/browse?limit=${limit}&offset=${offset}`,
      ).then((r) => r.results ?? []),
  });
}

export function useCatalogSearch(conn: string, query: string, opts: { limit?: number } = {}) {
  const limit = opts.limit ?? 25;
  const q = query.trim();
  return useQuery({
    queryKey: keys.catalogSearch(conn, q, limit),
    enabled: !!conn && q.length >= MIN_SEARCH_LEN,
    queryFn: () =>
      apiFetch<{ results: TableSearchResult[] }>(
        `${base(conn)}/catalog/search?q=${enc(q)}&limit=${limit}`,
      ).then((r) => r.results ?? []),
  });
}

export function useCatalogEntity(conn: string, urn: string | null) {
  return useQuery({
    queryKey: keys.entity(conn, urn ?? ""),
    enabled: !!conn && !!urn,
    queryFn: () => apiFetch<CatalogEntity>(`${base(conn)}/catalog/entity?urn=${enc(urn!)}`),
  });
}

// --- catalog metadata pickers (#785) ---

// useTagLookup name-searches tags for the tag picker. Disabled below the search
// threshold so it does not fire on the first keystroke.
export function useTagLookup(conn: string, query: string) {
  const q = query.trim();
  return useQuery({
    queryKey: keys.lookupTags(conn, q),
    enabled: !!conn && q.length >= MIN_SEARCH_LEN,
    queryFn: () =>
      apiFetch<{ results: EntityRef[] }>(
        `${base(conn)}/catalog/lookup/tags?q=${enc(q)}&limit=10`,
      ).then((r) => r.results ?? []),
  });
}

// useGlossaryLookup name-searches glossary terms for the glossary picker.
export function useGlossaryLookup(conn: string, query: string) {
  const q = query.trim();
  return useQuery({
    queryKey: keys.lookupGlossary(conn, q),
    enabled: !!conn && q.length >= MIN_SEARCH_LEN,
    queryFn: () =>
      apiFetch<{ results: EntityRef[] }>(
        `${base(conn)}/catalog/lookup/glossary-terms?q=${enc(q)}&limit=10`,
      ).then((r) => r.results ?? []),
  });
}

// useDomainLookup lists all domains for the domain picker; DataHub has no
// name-scoped domain search, so the picker filters the full list client-side.
export function useDomainLookup(conn: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.lookupDomains(conn),
    enabled: !!conn && enabled,
    queryFn: () =>
      apiFetch<{ results: EntityRef[] }>(`${base(conn)}/catalog/lookup/domains`).then(
        (r) => r.results ?? [],
      ),
  });
}

// --- catalog writes ---

function useInvalidateEntity(conn: string) {
  const qc = useQueryClient();
  return (urn: string) => {
    void qc.invalidateQueries({ queryKey: keys.entity(conn, urn) });
    void qc.invalidateQueries({ queryKey: ["datahub", conn, "catalog"] });
  };
}

export function useUpdateDescription(conn: string) {
  const invalidate = useInvalidateEntity(conn);
  return useMutation({
    mutationFn: (v: { urn: string; description: string }) =>
      apiFetch(`${base(conn)}/catalog/entity/description`, {
        method: "PUT",
        body: JSON.stringify(v),
      }),
    onSuccess: (_d, v) => invalidate(v.urn),
  });
}

export function useUpdateTags(conn: string) {
  const invalidate = useInvalidateEntity(conn);
  return useMutation({
    mutationFn: (v: { urn: string; add?: string[]; remove?: string[] }) =>
      apiFetch(`${base(conn)}/catalog/entity/tags`, { method: "PUT", body: JSON.stringify(v) }),
    onSuccess: (_d, v) => invalidate(v.urn),
  });
}

export function useUpdateGlossaryTerms(conn: string) {
  const invalidate = useInvalidateEntity(conn);
  return useMutation({
    mutationFn: (v: { urn: string; add?: string[]; remove?: string[] }) =>
      apiFetch(`${base(conn)}/catalog/entity/glossary-terms`, {
        method: "PUT",
        body: JSON.stringify(v),
      }),
    onSuccess: (_d, v) => invalidate(v.urn),
  });
}

export function useUpdateOwners(conn: string) {
  const invalidate = useInvalidateEntity(conn);
  return useMutation({
    mutationFn: (v: { urn: string; add_owners?: OwnerChange[]; remove?: string[] }) =>
      apiFetch(`${base(conn)}/catalog/entity/owners`, { method: "PUT", body: JSON.stringify(v) }),
    onSuccess: (_d, v) => invalidate(v.urn),
  });
}

export function useUpdateDomain(conn: string) {
  const invalidate = useInvalidateEntity(conn);
  return useMutation({
    mutationFn: (v: { urn: string; domain?: string; clear_domain?: boolean }) =>
      apiFetch(`${base(conn)}/catalog/entity/domain`, { method: "PUT", body: JSON.stringify(v) }),
    onSuccess: (_d, v) => invalidate(v.urn),
  });
}

// --- context documents ---

export function useDocumentsBrowse(conn: string, opts: { limit?: number; offset?: number } = {}) {
  const limit = opts.limit ?? 50;
  const offset = opts.offset ?? 0;
  return useQuery({
    queryKey: keys.docsBrowse(conn, limit, offset),
    enabled: !!conn,
    queryFn: () =>
      apiFetch<{ documents: ContextDocument[]; total: number }>(
        `${base(conn)}/documents/browse?limit=${limit}&offset=${offset}`,
      ),
  });
}

export function useDocumentsSearch(conn: string, query: string, opts: { limit?: number } = {}) {
  const limit = opts.limit ?? 25;
  const q = query.trim();
  return useQuery({
    queryKey: keys.docsSearch(conn, q, limit),
    enabled: !!conn && q.length >= MIN_SEARCH_LEN,
    queryFn: () =>
      apiFetch<{ documents: ContextDocument[] }>(
        `${base(conn)}/documents/search?q=${enc(q)}&limit=${limit}`,
      ).then((r) => r.documents ?? []),
  });
}

export function useDocument(conn: string, id: string | null) {
  return useQuery({
    queryKey: keys.doc(conn, id ?? ""),
    enabled: !!conn && !!id,
    queryFn: () => apiFetch<ContextDocument>(`${base(conn)}/documents/${enc(id!)}`),
  });
}

function useInvalidateDocs(conn: string) {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: ["datahub", conn, "documents"] });
}

export interface DocumentInput {
  entity_urn?: string;
  title: string;
  content: string;
  category?: string;
}

export function useCreateDocument(conn: string) {
  const invalidate = useInvalidateDocs(conn);
  return useMutation({
    mutationFn: (v: DocumentInput) =>
      apiFetch<ContextDocument>(`${base(conn)}/documents`, {
        method: "POST",
        body: JSON.stringify(v),
      }),
    onSuccess: () => invalidate(),
  });
}

export function useUpdateDocument(conn: string) {
  const invalidate = useInvalidateDocs(conn);
  return useMutation({
    mutationFn: (v: { id: string } & DocumentInput) =>
      apiFetch<ContextDocument>(`${base(conn)}/documents/${enc(v.id)}`, {
        method: "PUT",
        body: JSON.stringify(v),
      }),
    onSuccess: () => invalidate(),
  });
}

export function useDeleteDocument(conn: string) {
  const invalidate = useInvalidateDocs(conn);
  return useMutation({
    // apiFetchRaw resolves the Response on any status, so a rejected DELETE must
    // be turned into a thrown error here or the mutation would report success.
    mutationFn: async (id: string) => {
      const res = await apiFetchRaw(`${base(conn)}/documents/${enc(id)}`, { method: "DELETE" });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new ApiError(res.status, body.detail || res.statusText, body);
      }
    },
    onSuccess: () => invalidate(),
  });
}
