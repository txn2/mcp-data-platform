import {
  useInfiniteQuery,
  type InfiniteData,
  type QueryKey,
} from "@tanstack/react-query";
import { useMemo } from "react";
import { apiFetch } from "../client";
import type { PaginatedResponse } from "../types";

// Shared offset-based infinite-pagination primitives (#972). The portal's list
// surfaces all page the same way: a `limit`/`offset` window over a stable
// created_at/updated_at DESC ordering, with the server returning a full-set
// `total`. These helpers accumulate those pages into one merged list plus the
// controls a "Load more" affordance and the useInfiniteScroll hook consume, so
// every list (assets, collections, feedback, resources, knowledge pages, users,
// changelog) shares one implementation rather than reinventing paging per page.

// nextOffset returns the offset for the next page: the count of rows already
// fetched (the server offset, not the de-duplicated row count), or undefined
// once every row has been fetched. The cap comes from the LATEST page's total
// so rows inserted after the first fetch are still reachable, and an empty
// trailing page ends pagination even if total is stale-high (rows deleted
// between the count and the fetch), so "Load more" can't spin forever.
export function nextOffset<T>(pages: PaginatedResponse<T>[]): number | undefined {
  const last = pages[pages.length - 1];
  if (last && last.data.length === 0) return undefined;
  const fetched = pages.reduce((n, p) => n + p.data.length, 0);
  return fetched < (last?.total ?? 0) ? fetched : undefined;
}

// InfiniteResult is the flattened view an infinite query exposes to list pages:
// a single accumulated page (all loaded rows merged) plus the controls needed to
// render a "Load more" affordance. `data` mirrors PaginatedResponse so a page
// keeps reading `.data.data` / `.data.total` exactly as it did for a single-page
// query, only gaining the load-more controls.
export interface InfiniteResult<T> {
  data: PaginatedResponse<T> | undefined;
  isLoading: boolean;
  isError: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  refetch: () => void;
}

// flattenPages merges every fetched page into one PaginatedResponse: rows are
// concatenated in fetch order and de-duplicated by keyOf (offset paging over a
// created_at DESC list can re-emit a row when inserts shift the window, which
// would otherwise collide React keys and overcount), total comes from the
// latest page (most current count), limit from the first, and per-row share
// summaries are unioned across pages. Returns undefined before the first page
// resolves so callers can distinguish "loading" from "empty".
export function flattenPages<T>(
  pages: InfiniteData<PaginatedResponse<T>> | undefined,
  keyOf: (item: T) => string,
): PaginatedResponse<T> | undefined {
  const list = pages?.pages;
  const first = list?.[0];
  if (!list || !first) return undefined;
  const last = list[list.length - 1] ?? first;

  const seen = new Set<string>();
  const data: T[] = [];
  for (const page of list) {
    for (const item of page.data) {
      const k = keyOf(item);
      if (seen.has(k)) continue;
      seen.add(k);
      data.push(item);
    }
  }

  return {
    data,
    total: last.total,
    limit: first.limit,
    offset: 0,
    share_summaries: Object.assign(
      {},
      ...list.map((p) => p.share_summaries ?? {}),
    ),
  };
}

// useInfiniteResult adapts a TanStack infinite query into the flattened
// InfiniteResult a list page consumes, so every surface shares one merge/return
// path.
export function useInfiniteResult<T>(
  q: {
    data: InfiniteData<PaginatedResponse<T>> | undefined;
    isLoading: boolean;
    isError: boolean;
    hasNextPage: boolean;
    isFetchingNextPage: boolean;
    fetchNextPage: () => unknown;
    refetch: () => unknown;
  },
  keyOf: (item: T) => string,
): InfiniteResult<T> {
  const data = useMemo(() => flattenPages(q.data, keyOf), [q.data, keyOf]);
  return {
    data,
    isLoading: q.isLoading,
    isError: q.isError,
    hasNextPage: q.hasNextPage,
    isFetchingNextPage: q.isFetchingNextPage,
    fetchNextPage: () => void q.fetchNextPage(),
    refetch: () => void q.refetch(),
  };
}

// toPaginated adapts a non-PaginatedResponse list envelope (e.g. resources
// `{resources,total}`, users `{users,total}`, knowledge pages `{pages,total}`)
// into the PaginatedResponse shape the shared helpers operate on, so those
// surfaces reuse the same flatten/nextOffset path as the natively-paginated
// ones. `offset` is the page param that produced this page.
export function toPaginated<T>(
  rows: T[] | undefined,
  total: number,
  limit: number,
  offset: number,
): PaginatedResponse<T> {
  return { data: rows ?? [], total, limit, offset };
}

// useOffsetInfiniteQuery is the one-call shared entry point: given a query key, a
// page size, a key extractor, and a fetcher that returns a PaginatedResponse for
// a given offset, it wires the TanStack infinite query (offset page param,
// nextOffset stop condition) and returns the flattened InfiniteResult. Surfaces
// whose endpoint returns a different envelope adapt it to PaginatedResponse
// inside `fetchPage` (see toPaginated).
export function useOffsetInfiniteQuery<T>(opts: {
  queryKey: QueryKey;
  pageSize: number;
  keyOf: (item: T) => string;
  fetchPage: (offset: number, limit: number) => Promise<PaginatedResponse<T>>;
  enabled?: boolean;
}): InfiniteResult<T> {
  const { queryKey, pageSize, keyOf, fetchPage, enabled } = opts;
  const q = useInfiniteQuery({
    queryKey,
    enabled,
    initialPageParam: 0,
    queryFn: ({ pageParam }) => fetchPage(pageParam, pageSize),
    getNextPageParam: (_last, all) => nextOffset(all),
  });
  return useInfiniteResult(q, keyOf);
}

// paginatedFetch is the common fetchPage body for endpoints that already return
// a PaginatedResponse: it appends limit/offset (plus any extra params) and
// fetches. Endpoints with a different envelope build their own fetchPage that
// maps the response through toPaginated.
export function paginatedFetch<T>(
  path: string,
  offset: number,
  limit: number,
  extra?: Record<string, string | undefined>,
): Promise<PaginatedResponse<T>> {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(extra ?? {})) {
    if (v !== undefined && v !== "") sp.set(k, v);
  }
  sp.set("limit", String(limit));
  sp.set("offset", String(offset));
  return apiFetch<PaginatedResponse<T>>(`${path}?${sp.toString()}`);
}
