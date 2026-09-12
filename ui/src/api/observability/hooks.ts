import { useQuery } from "@tanstack/react-query";
import { apiFetchAt, ApiError } from "@/api/admin/client";
import type { PromVectorResponse, PromMatrixResponse } from "./types";

// OBSERVABILITY_BASE is the PromQL proxy mount point. It is NOT under
// the admin base; the proxy gates on the observability:read persona
// capability independently.
const OBSERVABILITY_BASE = "/api/v1/observability";

// PROM_STALE_TIME matches Prometheus's default scrape interval, so the
// portal does not refetch faster than the data can change.
const PROM_STALE_TIME = 30_000;

// isBackendUnconfigured reports whether an error is a metrics-backend
// availability failure. The proxy answers 503 whether the backend is not
// configured, unreachable, or timed out, naming which in the body (a CDN in
// front of a deployment replaces the body of a 502 or 504, #1704); 502 and
// 504 are still accepted because a proxy between the portal and the platform
// can answer them itself. The views render these as a "metrics unavailable"
// empty state rather than a hard error.
export function isBackendUnconfigured(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    (err.status === 503 || err.status === 502 || err.status === 504)
  );
}

// useObservabilityQuery runs an instant PromQL query. An empty query
// string disables the hook (used while a drilldown selection is
// pending). enabled lets callers gate on a selected dimension.
export function useObservabilityQuery(query: string, opts?: { enabled?: boolean }) {
  return useQuery({
    queryKey: ["observability", "query", query],
    queryFn: () =>
      apiFetchAt<PromVectorResponse>(
        OBSERVABILITY_BASE,
        `/query?query=${encodeURIComponent(query)}`,
      ),
    enabled: (opts?.enabled ?? true) && query !== "",
    staleTime: PROM_STALE_TIME,
    refetchInterval: PROM_STALE_TIME,
    retry: false,
  });
}

// useObservabilityQueryRange runs a range PromQL query for timeseries
// charts. start/end are unix seconds; step is the resolution in seconds.
export function useObservabilityQueryRange(
  query: string,
  start: number,
  end: number,
  step: number,
  opts?: { enabled?: boolean },
) {
  return useQuery({
    queryKey: ["observability", "query_range", query, start, end, step],
    queryFn: () =>
      apiFetchAt<PromMatrixResponse>(
        OBSERVABILITY_BASE,
        `/query_range?query=${encodeURIComponent(query)}&start=${start}&end=${end}&step=${step}`,
      ),
    enabled: (opts?.enabled ?? true) && query !== "",
    staleTime: PROM_STALE_TIME,
    refetchInterval: PROM_STALE_TIME,
    retry: false,
  });
}
