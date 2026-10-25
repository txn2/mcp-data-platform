import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  AdminEndpointDetail,
  AdminEndpointList,
  AdminEndpointListParams,
  EndpointRange,
} from "../types";
import { REFETCH_INTERVAL } from "./shared";

// Every endpoint binding with its traffic (#2208).

export const adminEndpointsKey = ["admin-endpoints"] as const;

/** endpointListQuery is the list route's query string for params. */
export function endpointListQuery(params: AdminEndpointListParams): string {
  const search = new URLSearchParams({ range: params.range });
  if (params.access) search.set("access", params.access);
  if (params.status) search.set("status", params.status);
  if (params.page && params.page > 1) search.set("page", String(params.page));
  if (params.perPage) search.set("per_page", String(params.perPage));
  return search.toString();
}

/**
 * useAdminEndpoints reads one page of the bindings the filters match, with
 * their traffic over the range, and how many match in all (#2251).
 */
export function useAdminEndpoints(params: AdminEndpointListParams) {
  return useQuery({
    queryKey: [...adminEndpointsKey, "list", params],
    queryFn: () => apiFetch<AdminEndpointList>(`/endpoints?${endpointListQuery(params)}`),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

/** useAdminEndpoint reads one endpoint's page over range. */
export function useAdminEndpoint(name: string, range: EndpointRange) {
  return useQuery({
    queryKey: [...adminEndpointsKey, name, range],
    queryFn: () =>
      apiFetch<AdminEndpointDetail>(
        `/endpoints/${encodeURIComponent(name)}?range=${range}`,
      ),
    enabled: !!name,
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}
