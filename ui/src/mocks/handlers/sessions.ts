import { http, HttpResponse } from "msw";
import type { SessionKind, SessionSummary } from "@/api/admin/types";
import { MOCK_CALLER_EMAIL, mockAuditEvents } from "../data/audit";
import { mockSessionDetail, mockSessions } from "../data/sessions";

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";

// The portal session routes are scoped to the caller server-side, so the mock
// scopes them too: a mock that returned every session would make the user
// surface look right while hiding the one behavior that surface exists to have.
// The caller is the audit fixture's own, not a second copy of the address.

/** Applies the list filters the sessions endpoint accepts. */
function applyFilters(url: URL, sessions: SessionSummary[]): SessionSummary[] {
  const userId = url.searchParams.get("user_id");
  const kind = url.searchParams.get("kind") as SessionKind | null;
  let filtered = sessions;
  if (userId) filtered = filtered.filter((s) => s.user_id === userId);
  if (kind) filtered = filtered.filter((s) => s.kind === kind);
  if (url.searchParams.get("has_assets") === "true") {
    filtered = filtered.filter((s) => s.asset_count > 0);
  }
  if (url.searchParams.get("has_failures") === "true") {
    filtered = filtered.filter((s) => s.failure_count > 0);
  }
  // The window bounds a session's EVENTS, so a session matches when any of
  // its calls falls inside it — not only when the whole session does.
  const start = url.searchParams.get("start_time");
  if (start) {
    filtered = filtered.filter(
      (s) => Date.parse(s.last_active_at) >= Date.parse(start),
    );
  }
  const end = url.searchParams.get("end_time");
  if (end) {
    filtered = filtered.filter(
      (s) => Date.parse(s.started_at) <= Date.parse(end),
    );
  }
  return filtered;
}

/** One page of a filtered session list, in the envelope both surfaces use. */
function listPage(url: URL, sessions: SessionSummary[]) {
  const page = parseInt(url.searchParams.get("page") ?? "1", 10);
  const perPage = parseInt(url.searchParams.get("per_page") ?? "25", 10);
  const filtered = applyFilters(url, sessions);
  const start = (page - 1) * perPage;

  return HttpResponse.json({
    data: filtered.slice(start, start + perPage),
    total: filtered.length,
    page,
    per_page: perPage,
  });
}

/** One session in full, or the not-found the server answers with. */
function detailOrNotFound(sessionID: string, url: URL, callerID?: string) {
  const page = parseInt(url.searchParams.get("page") ?? "1", 10);
  const perPage = parseInt(url.searchParams.get("per_page") ?? "25", 10);
  const detail = mockSessionDetail(sessionID, page, perPage);
  // A session that is not the caller's own is not found, not forbidden: the
  // portal must not tell a reader that someone else's session exists.
  if (!detail || (callerID && detail.user_id !== callerID)) {
    return HttpResponse.json(
      { title: "Not Found", status: 404, detail: "session not found" },
      { status: 404 },
    );
  }
  return HttpResponse.json(detail);
}

export const sessionHandlers = [
  http.get(`${ADMIN_BASE}/sessions`, ({ request }) =>
    listPage(new URL(request.url), mockSessions),
  ),

  http.get(`${ADMIN_BASE}/sessions/:id`, ({ params, request }) =>
    detailOrNotFound(String(params.id), new URL(request.url)),
  ),

  // The caller's own sessions. Scoped here the way the server scopes them.
  http.get(`${PORTAL_BASE}/sessions`, ({ request }) =>
    listPage(
      new URL(request.url),
      mockSessions.filter((s) => s.user_id === MOCK_CALLER_EMAIL),
    ),
  ),

  http.get(`${PORTAL_BASE}/sessions/:id`, ({ params, request }) =>
    detailOrNotFound(String(params.id), new URL(request.url), MOCK_CALLER_EMAIL),
  ),

  // One audit event by id: the session timeline opens the event drawer, which
  // needs the whole event, not the timeline row.
  http.get(`${ADMIN_BASE}/audit/events/:id`, ({ params }) => {
    const event = mockAuditEvents.find((e) => e.id === String(params.id));
    if (!event) {
      return HttpResponse.json(
        { title: "Not Found", status: 404, detail: "audit event not found" },
        { status: 404 },
      );
    }
    return HttpResponse.json(event);
  }),
];
