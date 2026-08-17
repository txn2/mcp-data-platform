import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  SessionDetail,
  SessionKind,
  SessionListResponse,
} from "@/api/admin/types";

// The caller's own sessions. The wire shape is the operator surface's, because
// it is the same read model narrowed to one caller: the types are imported
// rather than restated so the two views cannot describe a session differently.
//
// There is no user parameter. The server scopes every read to the
// authenticated caller and answers another user's session id as not-found, so
// a user facet here would be a control that does nothing.

export interface MySessionListParams {
  page?: number;
  perPage?: number;
  kind?: SessionKind | "";
  hasAssets?: boolean;
  hasFailures?: boolean;
  startTime?: string;
  endTime?: string;
}

/** Builds the query string the portal sessions list endpoint accepts. */
function sessionQuery(params: MySessionListParams): string {
  const search = new URLSearchParams();
  if (params.page) search.set("page", String(params.page));
  if (params.perPage) search.set("per_page", String(params.perPage));
  if (params.kind) search.set("kind", params.kind);
  if (params.hasAssets) search.set("has_assets", "true");
  if (params.hasFailures) search.set("has_failures", "true");
  if (params.startTime) search.set("start_time", params.startTime);
  if (params.endTime) search.set("end_time", params.endTime);
  const qs = search.toString();
  return qs ? `?${qs}` : "";
}

export function useMySessions(params: MySessionListParams = {}) {
  return useQuery({
    queryKey: ["my-sessions", params],
    queryFn: () =>
      apiFetch<SessionListResponse>(`/sessions${sessionQuery(params)}`),
    // No poll: the list is a rollup over every event in the window, and a
    // session is read after the fact. It refetches on focus and on any
    // filter or page change, and paging must not blank the table meanwhile.
    placeholderData: keepPreviousData,
  });
}

export function useMySession(sessionId: string, page = 1, perPage = 25) {
  return useQuery({
    queryKey: ["my-sessions", sessionId, page, perPage],
    queryFn: () =>
      apiFetch<SessionDetail>(
        `/sessions/${encodeURIComponent(sessionId)}?page=${page}&per_page=${perPage}`,
      ),
    enabled: Boolean(sessionId),
    placeholderData: keepPreviousData,
  });
}
