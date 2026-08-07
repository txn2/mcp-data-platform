// DataHub Catalog and Context Docs API (#719/#720). Thin typed hooks over the
// portal DataHub REST surface (#718) at /api/v1/portal/datahub/{connection}/...
//
// This is the one import path for the whole surface: the response types and URL
// helpers live in ./datahubCore and the glossary in ./datahubGlossary, both
// re-exported here, so a caller never has to know which module a hook is in.
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "./client";
import { MIN_SEARCH_LEN, base, catalogKey, enc } from "./datahubCore";
import type {
  ContextDocument,
  CatalogEntity,
  DataHubConnection,
  EntityRef,
  OwnerChange,
  TableSearchResult,
} from "./datahubCore";

export * from "./datahubCore";
export * from "./datahubGlossary";

// --- query keys ---

const keys = {
  connections: ["datahub", "connections"] as const,
  catalogBrowse: (conn: string, limit: number, offset: number) =>
    ["datahub", conn, "catalog", "browse", limit, offset] as const,
  catalogSearch: (conn: string, q: string, limit: number) =>
    ["datahub", conn, "catalog", "search", q, limit] as const,
  entity: (conn: string, urn: string) => ["datahub", conn, "catalog", "entity", urn] as const,
  lookupTags: (conn: string, q: string) => ["datahub", conn, "catalog", "lookup", "tags", q] as const,
  tagList: (conn: string, q: string) => ["datahub", conn, "catalog", "tags", q] as const,
  tagUsage: (conn: string, urn: string) => ["datahub", conn, "catalog", "tags", "usage", urn] as const,
  lookupGlossary: (conn: string, q: string) =>
    ["datahub", conn, "catalog", "lookup", "glossary", q] as const,
  lookupDomains: (conn: string) => ["datahub", conn, "catalog", "lookup", "domains"] as const,
  domainList: (conn: string) => ["datahub", conn, "catalog", "domains"] as const,
  domainMembers: (conn: string, urn: string) =>
    ["datahub", conn, "catalog", "domains", "members", urn] as const,
  docsBrowse: (conn: string, limit: number, offset: number) =>
    ["datahub", conn, "documents", "browse", limit, offset] as const,
  docsSearch: (conn: string, q: string, limit: number) =>
    ["datahub", conn, "documents", "search", q, limit] as const,
  doc: (conn: string, id: string) => ["datahub", conn, "documents", id] as const,
};

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

// --- tag governance (#1156) ---

// TAG_LIST_LIMIT is the page the Tags surface asks for. It is the largest page
// the read actually returns: the DataHub adapter clamps a ref lookup to
// maxRefLimit (pkg/semantic/datahub/adapter.go), so asking for more would report
// a page size the server never honours. A full page means there may be more, and
// the surface says so rather than presenting a truncated list as complete.
export const TAG_LIST_LIMIT = 100;

// useTagList lists the tags on a connection for the Tags surface, optionally
// name-filtered. It shares the picker's lookup route (the backend read is the
// same one) but not its query key or its minimum-length gate: the management
// list renders every tag before anything is typed.
export function useTagList(conn: string, query: string) {
  const q = query.trim();
  return useQuery({
    queryKey: keys.tagList(conn, q),
    enabled: !!conn,
    queryFn: () =>
      apiFetch<{ results: EntityRef[] }>(
        `${base(conn)}/catalog/lookup/tags?q=${enc(q)}&limit=${TAG_LIST_LIMIT}`,
      ).then((r) => r.results ?? []),
  });
}

// useTagUsage lists the datasets carrying a tag, through the catalog search's
// tags filter (the adapter maps it to DataHub's `tags` filter field). The query
// is "*" because the filter, not the text, selects the rows.
export function useTagUsage(conn: string, urn: string | null) {
  return useQuery({
    queryKey: keys.tagUsage(conn, urn ?? ""),
    enabled: !!conn && !!urn,
    queryFn: () =>
      apiFetch<{ results: TableSearchResult[] }>(
        `${base(conn)}/catalog/search?q=*&tags=${enc(urn!)}&limit=${TAG_LIST_LIMIT}`,
      ).then((r) => r.results ?? []),
  });
}

// useInvalidateTags drops every cached catalog read for the connection. The tag
// list, the pickers, and each entity's tag chips all read tag state, so a tag
// definition change has to reach all of them.
function useInvalidateTags(conn: string) {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: catalogKey(conn) });
}

// useCreateTag defines a new tag. DataHub indexes tags asynchronously, so the
// tag may not appear in a re-listed page immediately; the returned URN is
// authoritative in the meantime.
export function useCreateTag(conn: string) {
  const invalidate = useInvalidateTags(conn);
  return useMutation({
    mutationFn: (v: { name: string; description?: string }) =>
      apiFetch<{ urn: string }>(`${base(conn)}/catalog/tags`, {
        method: "POST",
        body: JSON.stringify(v),
      }),
    onSuccess: () => invalidate(),
  });
}

export function useDeleteTag(conn: string) {
  const invalidate = useInvalidateTags(conn);
  return useMutation({
    // apiFetchRaw resolves the Response on any status, so a rejected DELETE must
    // be turned into a thrown error here or the mutation would report success.
    mutationFn: async (urn: string) => {
      const res = await apiFetchRaw(`${base(conn)}/catalog/tags?urn=${enc(urn)}`, {
        method: "DELETE",
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new ApiError(res.status, body.detail || res.statusText, body);
      }
    },
    onSuccess: () => invalidate(),
  });
}

// --- domain governance (#1157) ---

// DOMAIN_LIST_LIMIT is the number of domains the list read can return. Unlike
// the tag list it is not a page this surface chooses: the upstream ListDomains
// GraphQL query hardcodes `count: 100` (mcp-datahub pkg/client/queries.go), and
// the lookup route takes no limit parameter, so 100 is the ceiling however the
// list is asked for. A full list therefore means there may be more, and the
// surface says so rather than presenting it as the whole set.
export const DOMAIN_LIST_LIMIT = 100;

// DOMAIN_MEMBER_LIMIT is the page the membership read asks for. The catalog
// search honours it (the handler caps at 200), so a full page is a floor on the
// membership, not a total.
export const DOMAIN_MEMBER_LIMIT = 100;

// useDomainList lists the domains on a connection for the Domains surface. It
// shares the picker's lookup route (the backend read is the same one) but not
// its query key or its `enabled` gate: the management list renders every domain
// unconditionally. DataHub has no name-scoped domain search, so filtering is
// client-side.
export function useDomainList(conn: string) {
  return useQuery({
    queryKey: keys.domainList(conn),
    enabled: !!conn,
    queryFn: () =>
      apiFetch<{ results: EntityRef[] }>(`${base(conn)}/catalog/lookup/domains`).then(
        (r) => r.results ?? [],
      ),
  });
}

// useDomainMembers lists the tables in a domain, through the catalog search's
// domain filter (the adapter maps it to DataHub's `domains` filter field). The
// query is "*" because the filter, not the text, selects the rows.
export function useDomainMembers(conn: string, urn: string | null) {
  return useQuery({
    queryKey: keys.domainMembers(conn, urn ?? ""),
    enabled: !!conn && !!urn,
    queryFn: () =>
      apiFetch<{ results: TableSearchResult[] }>(
        `${base(conn)}/catalog/search?q=*&domain=${enc(urn!)}&limit=${DOMAIN_MEMBER_LIMIT}`,
      ).then((r) => r.results ?? []),
  });
}

// useInvalidateDomains drops every cached catalog read for the connection. The
// domain list, the domain picker, each domain's membership, and each entity's
// domain chip all read domain state, so a domain change has to reach all of them.
function useInvalidateDomains(conn: string) {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: catalogKey(conn) });
}

// useCreateDomain defines a new domain. DataHub indexes domains asynchronously,
// so the domain may not appear in a re-listed page immediately; the returned URN
// is authoritative in the meantime.
export function useCreateDomain(conn: string) {
  const invalidate = useInvalidateDomains(conn);
  return useMutation({
    mutationFn: (v: { name: string; description?: string }) =>
      apiFetch<{ urn: string }>(`${base(conn)}/catalog/domains`, {
        method: "POST",
        body: JSON.stringify(v),
      }),
    onSuccess: () => invalidate(),
  });
}

export function useDeleteDomain(conn: string) {
  const invalidate = useInvalidateDomains(conn);
  return useMutation({
    // apiFetchRaw resolves the Response on any status, so a rejected DELETE must
    // be turned into a thrown error here or the mutation would report success.
    mutationFn: async (urn: string) => {
      const res = await apiFetchRaw(`${base(conn)}/catalog/domains?urn=${enc(urn)}`, {
        method: "DELETE",
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new ApiError(res.status, body.detail || res.statusText, body);
      }
    },
    onSuccess: () => invalidate(),
  });
}

// --- catalog writes ---

function useInvalidateEntity(conn: string) {
  const qc = useQueryClient();
  return (urn: string) => {
    void qc.invalidateQueries({ queryKey: keys.entity(conn, urn) });
    void qc.invalidateQueries({ queryKey: catalogKey(conn) });
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
