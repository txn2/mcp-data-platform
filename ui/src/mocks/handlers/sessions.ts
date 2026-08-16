import { http, HttpResponse } from "msw";
import type { SessionKind, SessionSummary } from "@/api/admin/types";
import { mockAuditEvents } from "../data/audit";
import { mockSessionDetail, mockSessions } from "../data/sessions";

const ADMIN_BASE = "/api/v1/admin";

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

export const sessionHandlers = [
  http.get(`${ADMIN_BASE}/sessions`, ({ request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "25", 10);
    const filtered = applyFilters(url, mockSessions);
    const start = (page - 1) * perPage;

    return HttpResponse.json({
      data: filtered.slice(start, start + perPage),
      total: filtered.length,
      page,
      per_page: perPage,
    });
  }),

  http.get(`${ADMIN_BASE}/sessions/:id`, ({ params, request }) => {
    const url = new URL(request.url);
    const page = parseInt(url.searchParams.get("page") ?? "1", 10);
    const perPage = parseInt(url.searchParams.get("per_page") ?? "25", 10);
    const detail = mockSessionDetail(String(params.id), page, perPage);
    if (!detail) {
      return HttpResponse.json(
        { title: "Not Found", status: 404, detail: "session not found" },
        { status: 404 },
      );
    }
    return HttpResponse.json(detail);
  }),

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
