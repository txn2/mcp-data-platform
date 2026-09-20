import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  AuditEvent,
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

// useMyAuditEvent reads one of the caller's own calls in full: the parameters it
// carried, the reason stated for it, and the error text when it failed, none
// of which the timeline entry holds (#1797).
//
// Named apart from useMyCall, which reads the call catalog: they are different
// records, and that difference is the reason this exists. It is not
// GET /portal/calls/{id}. A call record is written only for the sql,
// api and graphql kinds, so most of a session's rows -- search,
// list_connections, manage_resource, manage_table -- have none, and a
// drill-down that opened for two rows in eleven is not a drill-down. This reads
// the audit event itself, scoped to the caller: somebody else's event is
// not-found, the same answer an id that was never issued gets.
export function useMyAuditEvent(eventId: string | null) {
  return useQuery({
    queryKey: ["my-audit-event", eventId],
    queryFn: () => apiFetch<AuditEvent>(`/events/${encodeURIComponent(eventId ?? "")}`),
    enabled: Boolean(eventId),
  });
}
