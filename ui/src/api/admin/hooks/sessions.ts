import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  SessionDetail,
  SessionKind,
  SessionListResponse,
} from "../types";

export interface SessionListParams {
  page?: number;
  perPage?: number;
  userId?: string;
  kind?: SessionKind | "";
  hasAssets?: boolean;
  hasFailures?: boolean;
  startTime?: string;
  endTime?: string;
}

/** Builds the query string the sessions list endpoint accepts. */
function sessionQuery(params: SessionListParams): string {
  const search = new URLSearchParams();
  if (params.page) search.set("page", String(params.page));
  if (params.perPage) search.set("per_page", String(params.perPage));
  if (params.userId) search.set("user_id", params.userId);
  if (params.kind) search.set("kind", params.kind);
  if (params.hasAssets) search.set("has_assets", "true");
  if (params.hasFailures) search.set("has_failures", "true");
  if (params.startTime) search.set("start_time", params.startTime);
  if (params.endTime) search.set("end_time", params.endTime);
  const qs = search.toString();
  return qs ? `?${qs}` : "";
}

export function useSessions(params: SessionListParams = {}) {
  return useQuery({
    queryKey: ["sessions", params],
    queryFn: () =>
      apiFetch<SessionListResponse>(`/sessions${sessionQuery(params)}`),
    // No poll: the list is a rollup over every event in the window, and a
    // session is read after the fact. It refetches on focus and on any
    // filter or page change, and paging must not blank the table meanwhile.
    placeholderData: keepPreviousData,
  });
}

export function useSession(sessionId: string, page = 1, perPage = 25) {
  return useQuery({
    queryKey: ["sessions", sessionId, page, perPage],
    queryFn: () =>
      apiFetch<SessionDetail>(
        `/sessions/${encodeURIComponent(sessionId)}?page=${page}&per_page=${perPage}`,
      ),
    enabled: Boolean(sessionId),
    placeholderData: keepPreviousData,
  });
}
