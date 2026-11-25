import { http, HttpResponse } from "msw";
import type {
  PlatformFeedbackDetail,
  PlatformFeedbackItem,
  PlatformFeedbackSettingsView,
  PlatformFeedbackStatus,
} from "@/api/admin/hooks";

// Platform feedback admin routes (#2820):
//   GET/PUT /settings/platform-feedback
//   GET     /platform-feedback            (list, with ?status, ?tool, ?q)
//   GET     /platform-feedback/:id        (one, with reports and deliveries)
//   PUT     /platform-feedback/:id/status
//   POST    /platform-feedback/:id/resend
// `?feedback=empty` on the document's URL empties the list, for a screenshot.

const ADMIN_BASE = "/api/v1/admin";
const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();

let settings: PlatformFeedbackSettingsView = {
  enabled: true,
  channels: [],
  personas: [],
  session_limit: 5,
  persona_daily_limit: 50,
  updated_by: "admin@example.com",
  updated_at: ago(2 * 24 * 60),
};

const posts: PlatformFeedbackDetail[] = [
  {
    id: "pfb_shared", trying_to: "List the assets shared with me",
    blocked_by: "manage_asset has no filter for items others shared. list returns only the caller's own assets.",
    tool: "manage_asset", suggestion: "A shared_with_me filter on list, or a search source for shared items.",
    status: "open", report_count: 7, created_at: ago(3 * 24 * 60), last_reported_at: ago(60),
    reports: [
      { persona: "analyst", calls: ["mcp:call:9f3c21", "mcp:call:9f3c27"], reported_at: ago(60) },
      { persona: "analyst", calls: ["mcp:call:8a0e14"], reported_at: ago(300) },
      { persona: "data-engineer", calls: ["mcp:call:71bd02"], reported_at: ago(3 * 24 * 60) },
    ],
    deliveries: [
      { channel: "ops-webhook", status: "sent", attempts: 1, sent_at: ago(3 * 24 * 60) },
      { channel: "platform-team", status: "failed", attempts: 3, last_error: "404 channel_not_found" },
    ],
  },
  {
    id: "pfb_csv", trying_to: "Register a CSV with a header row in Excel format",
    blocked_by: "manage_table refused the same file twice: unsupported encoding",
    tool: "manage_table", suggestion: "", status: "open", report_count: 3,
    created_at: ago(26 * 60), last_reported_at: ago(240),
    reports: [{ persona: "analyst", calls: null, reported_at: ago(240) }],
    deliveries: [{ channel: "ops-webhook", status: "failed", attempts: 3, last_error: "502 Bad Gateway" }],
  },
  {
    id: "pfb_persona", trying_to: "Assign a persona to a new user", blocked_by: "No tool assigns personas",
    tool: "", suggestion: "", status: "planned", report_count: 2, created_at: ago(2 * 24 * 60), last_reported_at: ago(24 * 60),
    reports: [{ persona: "admin", calls: [], reported_at: ago(24 * 60) }],
    deliveries: [{ channel: "ops-webhook", status: "sent", attempts: 1, sent_at: ago(2 * 24 * 60) }],
  },
  {
    id: "pfb_bytes", trying_to: "Read a 40 MB export's bytes", blocked_by: "fetch caps the size it returns",
    tool: "fetch", suggestion: "", status: "declined", report_count: 1, created_at: ago(4 * 24 * 60), last_reported_at: ago(4 * 24 * 60),
    reports: [{ persona: "analyst", calls: ["mcp:call:5c11aa"], reported_at: ago(4 * 24 * 60) }],
    deliveries: [],
  },
];

function item(p: PlatformFeedbackDetail): PlatformFeedbackItem {
  const { reports: _r, deliveries: _d, ...rest } = p;
  return rest;
}

function emptied(): boolean {
  return typeof window !== "undefined" && new URLSearchParams(window.location.search).get("feedback") === "empty";
}

const notFound = () => HttpResponse.json({ detail: "no such platform feedback post" }, { status: 404 });

export const platformFeedbackHandlers = [
  http.get(`${ADMIN_BASE}/settings/platform-feedback`, () => HttpResponse.json(settings)),
  http.put(`${ADMIN_BASE}/settings/platform-feedback`, async ({ request }) => {
    const body = (await request.json()) as Partial<PlatformFeedbackSettingsView>;
    settings = { ...settings, ...body, updated_by: "admin@example.com", updated_at: new Date().toISOString() };
    return HttpResponse.json(settings);
  }),
  http.get(`${ADMIN_BASE}/platform-feedback`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    const status = q.get("status") ?? "";
    const tool = q.get("tool") ?? "";
    const text = (q.get("q") ?? "").toLowerCase();
    const shown = emptied()
      ? []
      : posts.filter(
          (p) =>
            (!status || p.status === status) &&
            (!tool || p.tool === tool) &&
            (!text || `${p.trying_to} ${p.blocked_by} ${p.suggestion}`.toLowerCase().includes(text)),
        );
    return HttpResponse.json({
      posts: shown.map(item),
      total: shown.length,
      stats: emptied()
        ? { open: 0, reports_this_week: 0, refused_this_week: 0, delivery_failures: 0 }
        : { open: posts.filter((p) => p.status === "open").length, reports_this_week: 19, refused_this_week: 2, delivery_failures: 2 },
    });
  }),
  http.get(`${ADMIN_BASE}/platform-feedback/:id`, ({ params }) => {
    const p = posts.find((x) => x.id === params.id);
    return p ? HttpResponse.json(p) : notFound();
  }),
  http.put(`${ADMIN_BASE}/platform-feedback/:id/status`, async ({ params, request }) => {
    const p = posts.find((x) => x.id === params.id);
    if (!p) return notFound();
    p.status = ((await request.json()) as { status: PlatformFeedbackStatus }).status;
    return new HttpResponse(null, { status: 204 });
  }),
  http.post(`${ADMIN_BASE}/platform-feedback/:id/resend`, async ({ params, request }) => {
    const p = posts.find((x) => x.id === params.id);
    if (!p) return notFound();
    const { channel } = (await request.json()) as { channel: string };
    const d = p.deliveries.find((x) => x.channel === channel);
    if (d) Object.assign(d, { status: "pending", last_error: undefined });
    return new HttpResponse(null, { status: 204 });
  }),
];
