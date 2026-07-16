import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import {
  useOffsetInfiniteQuery,
  toPaginated,
  type InfiniteResult,
} from "./infinite";
import type { ResolvedRef } from "@/lib/entityRefs";
import type {
  KnowledgePage,
  KnowledgePageInput,
  KnowledgePageListResponse,
  KnowledgePageVersionsResponse,
  ScoredKnowledgePage,
  SearchResponse,
} from "../types";
import { MIN_SEARCH_LEN } from "./shared";

// KNOWLEDGE_PAGE_SIZE is the number of knowledge pages requested per page. It is
// set to the store's search-limit cap (100): any larger value collapses to the
// store's small fallback (limit over 100 falls back to 20), so 100 is the
// largest honored window. This makes the first page cover a whole knowledgebase
// of up to 100 pages, strictly more than the previous fixed `limit: 200`
// request (which the store silently collapsed to 20), while still paginating
// beyond that. The tag facet is derived from the loaded pages and so widens as
// more load (#972).
export const KNOWLEDGE_PAGE_SIZE = 100;

const knowledgePageKey = (p: KnowledgePage): string => p.id;

// useInfiniteKnowledgePages accumulates knowledge-page list results so a
// deployment with more than one page of pages can reach all of them. The list
// endpoint returns a `{pages,total}` envelope, adapted to the shared
// PaginatedResponse shape here.
export function useInfiniteKnowledgePages(params?: {
  tag?: string;
  q?: string;
}): InfiniteResult<KnowledgePage> {
  return useOffsetInfiniteQuery<KnowledgePage>({
    queryKey: ["knowledge-pages", "infinite", params],
    pageSize: KNOWLEDGE_PAGE_SIZE,
    keyOf: knowledgePageKey,
    fetchPage: (offset, limit) => {
      const sp = new URLSearchParams();
      if (params?.tag) sp.set("tag", params.tag);
      if (params?.q) sp.set("q", params.q);
      sp.set("limit", String(limit));
      sp.set("offset", String(offset));
      return apiFetch<KnowledgePageListResponse>(
        `/knowledge-pages?${sp.toString()}`,
      ).then((r) => toPaginated(r.pages, r.total, limit, offset));
    },
  });
}

// --- Unified knowledge search (#661) ---

// useSearch fans one query across every source the caller can access (internal
// knowledge pages, the DataHub catalog, memory, insights, assets, prompts,
// endpoints, connections), returning results grouped by source with a coverage
// summary. It is the REST surface over the same router behind the MCP search
// tool. Disabled (no request) until query or entityUrns is non-empty, so an
// empty query falls back to the page's browse experience.
export function useSearch(
  query: string,
  params?: { entityUrns?: string[]; sources?: string[]; status?: string; limit?: number },
) {
  const q = query.trim();
  const sp = new URLSearchParams();
  if (q) sp.set("q", q);
  for (const urn of params?.entityUrns ?? []) sp.append("entity_urns", urn);
  for (const src of params?.sources ?? []) sp.append("sources", src);
  if (params?.status) sp.set("status", params.status);
  if (params?.limit) sp.set("limit", String(params.limit));

  const hasEntityURNs = (params?.entityUrns?.length ?? 0) > 0;
  return useQuery({
    queryKey: ["unified-search", q, params],
    // Free-text searches wait for the minimum query length; an entity-URN lookup
    // is exact, so it is exempt.
    enabled: q.length >= MIN_SEARCH_LEN || hasEntityURNs,
    queryFn: () => apiFetch<SearchResponse>(`/search?${sp.toString()}`),
  });
}

// --- Knowledge pages (#633) ---

export function useKnowledgePage(id: string | null) {
  return useQuery({
    queryKey: ["knowledge-page", id],
    queryFn: () => apiFetch<KnowledgePage>(`/knowledge-pages/${id}`),
    enabled: !!id,
  });
}

export function useSearchKnowledgePages(query: string, params?: { limit?: number }) {
  const q = query.trim();
  const search = new URLSearchParams({ q });
  if (params?.limit) search.set("limit", String(params.limit));
  return useQuery({
    queryKey: ["search-knowledge-pages", q, params],
    queryFn: () => apiFetch<ScoredKnowledgePage[]>(`/knowledge-pages/search?${search.toString()}`),
    enabled: q.length >= MIN_SEARCH_LEN,
  });
}

export function useKnowledgePageVersions(id: string | null) {
  return useQuery({
    queryKey: ["knowledge-page-versions", id],
    queryFn: () => apiFetch<KnowledgePageVersionsResponse>(`/knowledge-pages/${id}/versions`),
    enabled: !!id,
  });
}

export function useCreateKnowledgePage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: KnowledgePageInput) =>
      apiFetch<KnowledgePage>(`/knowledge-pages`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["knowledge-pages"] });
    },
  });
}

export function useUpdateKnowledgePage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: KnowledgePageInput }) =>
      apiFetch<KnowledgePage>(`/knowledge-pages/${id}`, { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["knowledge-pages"] });
      void qc.invalidateQueries({ queryKey: ["knowledge-page", vars.id] });
      void qc.invalidateQueries({ queryKey: ["knowledge-page-versions", vars.id] });
    },
  });
}

export function useDeleteKnowledgePage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      // apiFetchRaw does not throw on non-2xx, so check explicitly: a failed
      // delete must reject (not silently fire onSuccess and look deleted).
      const res = await apiFetchRaw(`/knowledge-pages/${id}`, { method: "DELETE" });
      if (!res.ok) {
        throw new Error(`Delete failed (${res.status})`);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["knowledge-pages"] });
    },
  });
}

/**
 * useResolveRefs resolves a batch of entity-reference URNs (mcp:/urn:li:) to
 * display labels and existence for inline knowledge-page chips (#664). Returns a
 * Map keyed by URN. Disabled when there are no references.
 */
export function useResolveRefs(urns: string[]) {
  // Stable key independent of order/duplication so identical ref sets share a cache entry.
  const key = Array.from(new Set(urns)).sort().join("\n");
  return useQuery({
    queryKey: ["knowledge-page-refs-resolve", key],
    queryFn: () =>
      apiFetch<{ refs: ResolvedRef[] }>("/knowledge-pages/refs/resolve", {
        method: "POST",
        body: JSON.stringify({ urns }),
      }),
    enabled: urns.length > 0,
    staleTime: 60_000,
    select: (data): Map<string, ResolvedRef> => {
      const map = new Map<string, ResolvedRef>();
      for (const r of data.refs) map.set(r.urn, r);
      return map;
    },
  });
}

/**
 * PageEntityRef is a knowledge page's reference, resolved and access-filtered by
 * the server: only references the viewer can access are returned, each with its
 * display label. The id of an inaccessible entity is never included.
 */
export interface PageEntityRef {
  urn: string;
  type: string;
  label: string;
  exists: boolean;
  source: string;
}

/** useKnowledgePageRefs lists a page's stored entity references (#664). */
export function useKnowledgePageRefs(id: string) {
  return useQuery({
    queryKey: ["knowledge-page-refs", id],
    queryFn: () => apiFetch<{ refs: PageEntityRef[] }>(`/knowledge-pages/${id}/refs`),
    enabled: !!id,
  });
}

export interface LineageInsight {
  id: string;
  text: string;
  category: string;
  status: string;
  confidence: string;
  captured_by: string;
}

export interface LineageChangeset {
  id: string;
  change_type: string;
  created_at: string;
  rolled_back: boolean;
  source_insight_ids: string[];
}

export interface KnowledgePageLineage {
  insights: LineageInsight[];
  changesets: LineageChangeset[];
}

/** useKnowledgePageLineage returns the insights a page was synthesized from (#678). */
export function useKnowledgePageLineage(id: string) {
  return useQuery({
    queryKey: ["knowledge-page-lineage", id],
    queryFn: () => apiFetch<KnowledgePageLineage>(`/knowledge-pages/${id}/lineage`),
    enabled: !!id,
  });
}

/**
 * useSetKnowledgePageRefs replaces a page's manual references with the given URNs
 * (promoted/inline refs are preserved server-side). Requires apply_knowledge.
 */
export function useSetKnowledgePageRefs(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (urns: string[]) =>
      apiFetch<{ refs: PageEntityRef[] }>(`/knowledge-pages/${id}/refs`, {
        method: "PUT",
        body: JSON.stringify({ refs: urns }),
      }),
    // Seed the cache from the response so a follow-up edit reads the new set
    // immediately (no stale-closure overwrite between mutations).
    onSuccess: (data) => {
      qc.setQueryData(["knowledge-page-refs", id], data);
    },
  });
}

/** KnowledgeBacklink is a knowledge page that references an entity (reverse lookup). */
export interface KnowledgeBacklink {
  id: string;
  slug: string;
  title: string;
}

/**
 * useKnowledgeBacklinks lists the knowledge pages that reference an entity (#664
 * Phase 4), so an entity view can surface "N knowledge pages reference this". The
 * server returns nothing for an entity the viewer cannot access.
 */
export function useKnowledgeBacklinks(urn: string | undefined) {
  return useQuery({
    queryKey: ["knowledge-backlinks", urn],
    queryFn: () =>
      apiFetch<{ pages: KnowledgeBacklink[] }>(
        `/knowledge-pages/backlinks?urn=${encodeURIComponent(urn ?? "")}`,
      ),
    enabled: !!urn,
  });
}
