import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  AdminEndpointDetail,
  AdminEndpointList,
  EndpointRange,
} from "../types";
import { REFETCH_INTERVAL } from "./shared";

// Every endpoint binding with its traffic (#2208).

export const adminEndpointsKey = ["admin-endpoints"] as const;

/** useAdminEndpoints lists every binding with its traffic over range. */
export function useAdminEndpoints(range: EndpointRange) {
  return useQuery({
    queryKey: [...adminEndpointsKey, "list", range],
    queryFn: () => apiFetch<AdminEndpointList>(`/endpoints?range=${range}`),
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
