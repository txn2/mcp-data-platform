// The business glossary (#1155 hierarchy, #1158 browser and editor): the tree of
// nodes and terms, what a term is applied to, and the documents attached to a
// catalog entity. Split from ./datahub, which re-exports it, so the DataHub API
// surface stays within the module size budget.
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "./client";
import {
  base,
  catalogKey,
  enc,
  type ContextDocument,
  type GlossaryChildren,
  type GlossaryRoots,
  type GlossaryNode,
  type TableSearchResult,
} from "./datahubCore";

const keys = {
  glossaryRoots: (conn: string) => [...catalogKey(conn), "glossary", "roots"] as const,
  glossaryChildren: (conn: string, urn: string) =>
    [...catalogKey(conn), "glossary", "children", urn] as const,
  glossaryParents: (conn: string, urn: string) =>
    [...catalogKey(conn), "glossary", "parents", urn] as const,
  glossaryUsage: (conn: string, urn: string, scope: string) =>
    [...catalogKey(conn), "glossary", "usage", scope, urn] as const,
  entityDocuments: (conn: string, urn: string) =>
    [...catalogKey(conn), "entity", "documents", urn] as const,
};

// GLOSSARY_PAGE_LIMIT is the page every glossary read asks for. It is the
// largest page the backend returns: the DataHub adapter clamps a glossary read
// to maxRefLimit (pkg/semantic/datahub/adapter.go), so asking for more would
// report a page size the server never honours. Each read reports its own total
// against it rather than presenting a first page as the whole branch.
export const GLOSSARY_PAGE_LIMIT = 100;

// useGlossaryRoots reads the top of the glossary: the nodes and the terms with
// no parent.
export function useGlossaryRoots(conn: string) {
  return useQuery({
    queryKey: keys.glossaryRoots(conn),
    enabled: !!conn,
    queryFn: () =>
      apiFetch<GlossaryRoots>(
        `${base(conn)}/catalog/glossary/roots?limit=${GLOSSARY_PAGE_LIMIT}`,
      ),
  });
}

// useGlossaryChildren reads one page of what sits directly under a node. It is
// disabled at the root, where useGlossaryRoots is the corresponding read.
export function useGlossaryChildren(conn: string, nodeUrn: string | null) {
  return useQuery({
    queryKey: keys.glossaryChildren(conn, nodeUrn ?? ""),
    enabled: !!conn && !!nodeUrn,
    queryFn: () =>
      apiFetch<GlossaryChildren>(
        `${base(conn)}/catalog/glossary/children?urn=${enc(nodeUrn!)}&limit=${GLOSSARY_PAGE_LIMIT}`,
      ),
  });
}

// useGlossaryParents reads an entity's ancestor nodes, direct parent first, so a
// term or node opened from anywhere can show where it sits without the caller
// having walked the tree to it.
export function useGlossaryParents(conn: string, urn: string | null) {
  return useQuery({
    queryKey: keys.glossaryParents(conn, urn ?? ""),
    enabled: !!conn && !!urn,
    queryFn: () =>
      apiFetch<{ parents: GlossaryNode[] }>(
        `${base(conn)}/catalog/glossary/parents?urn=${enc(urn!)}`,
      ).then((r) => r.parents ?? []),
  });
}

// useGlossaryTermUsage lists the tables a term is applied to. DataHub's
// glossaryTerms filter matches a table carrying the term on the TABLE or on one
// of its COLUMNS, so this is every carrier; useGlossaryTermColumnUsage narrows
// to the column-level ones. There is no table-level-only filter field, which is
// why the distinction takes two reads rather than one.
export function useGlossaryTermUsage(conn: string, urn: string | null) {
  return useGlossaryUsage(conn, urn, "glossary_term", "all");
}

// useGlossaryTermColumnUsage lists the tables where a COLUMN carries the term.
export function useGlossaryTermColumnUsage(conn: string, urn: string | null) {
  return useGlossaryUsage(conn, urn, "column_glossary_term", "column");
}

// useGlossaryUsage is the shared term-usage read: the same catalog search under
// a different glossary-term filter. The query is "*" because the filter, not the
// text, selects the rows.
function useGlossaryUsage(conn: string, urn: string | null, param: string, scope: string) {
  return useQuery({
    queryKey: keys.glossaryUsage(conn, urn ?? "", scope),
    enabled: !!conn && !!urn,
    queryFn: () =>
      apiFetch<{ results: TableSearchResult[] }>(
        `${base(conn)}/catalog/search?q=*&${param}=${enc(urn!)}&limit=${GLOSSARY_PAGE_LIMIT}`,
      ).then((r) => r.results ?? []),
  });
}

// useEntityDocuments reads the context documents attached to one catalog entity.
// The corpus-wide document browse and search cannot express it: neither is
// scoped to what a given entity carries.
export function useEntityDocuments(conn: string, urn: string | null) {
  return useQuery({
    queryKey: keys.entityDocuments(conn, urn ?? ""),
    enabled: !!conn && !!urn,
    queryFn: () =>
      apiFetch<{ documents: ContextDocument[] }>(
        `${base(conn)}/catalog/entity/documents?urn=${enc(urn!)}`,
      ).then((r) => r.documents ?? []),
  });
}

// GlossaryEntityInput creates a term or a node. An omitted parent_node creates
// it at the root of the glossary.
export interface GlossaryEntityInput {
  name: string;
  definition?: string;
  parent_node?: string;
}

// useInvalidateGlossary drops every cached catalog read for the connection. The
// hierarchy, the glossary picker, and each entity's term chips all read glossary
// state, so a glossary change has to reach all of them.
function useInvalidateGlossary(conn: string) {
  const qc = useQueryClient();
  return () => void qc.invalidateQueries({ queryKey: catalogKey(conn) });
}

// useCreateGlossaryTerm and useCreateGlossaryNode add to the glossary. DataHub
// populates the graph index asynchronously, so a new entity may not appear under
// its parent immediately; the returned URN is authoritative in the meantime.
export function useCreateGlossaryTerm(conn: string) {
  return useCreateGlossaryEntity(conn, "terms");
}

export function useCreateGlossaryNode(conn: string) {
  return useCreateGlossaryEntity(conn, "nodes");
}

function useCreateGlossaryEntity(conn: string, path: "terms" | "nodes") {
  const invalidate = useInvalidateGlossary(conn);
  return useMutation({
    mutationFn: (v: GlossaryEntityInput) =>
      apiFetch<{ urn: string }>(`${base(conn)}/catalog/glossary/${path}`, {
        method: "POST",
        body: JSON.stringify(v),
      }),
    onSuccess: () => invalidate(),
  });
}

// useDeleteGlossaryEntity retires a term or a node. Both go through one route
// because upstream is one call, and it removes neither a node's children nor a
// term's assignments.
export function useDeleteGlossaryEntity(conn: string) {
  const invalidate = useInvalidateGlossary(conn);
  return useMutation({
    // apiFetchRaw resolves the Response on any status, so a rejected DELETE must
    // be turned into a thrown error here or the mutation would report success.
    mutationFn: async (urn: string) => {
      const res = await apiFetchRaw(`${base(conn)}/catalog/glossary/entity?urn=${enc(urn)}`, {
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
