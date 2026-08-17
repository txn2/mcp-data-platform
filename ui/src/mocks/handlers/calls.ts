import { http, HttpResponse } from "msw";
import type { CallRecord } from "@/api/admin/types";
import { MOCK_CALLER_EMAIL } from "../data/audit";
import { mockCallRecords } from "../data/calls";

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";

// The portal call routes are scoped to the caller server-side, so the mock
// scopes them too: a mock that returned every record would make the user
// surface look right while hiding the one behavior that surface exists to have.

/** Applies the list facets both call endpoints accept. */
function applyFilters(url: URL, records: CallRecord[]): CallRecord[] {
  let filtered = records;
  const equals: [string, (r: CallRecord) => string | undefined][] = [
    ["user_id", (r) => r.user_id],
    ["kind", (r) => r.kind],
    ["connection", (r) => r.connection],
    ["outcome", (r) => r.outcome],
    ["session_id", (r) => r.session_id],
  ];
  for (const [param, read] of equals) {
    const want = url.searchParams.get(param);
    if (want) filtered = filtered.filter((r) => read(r) === want);
  }
  const target = url.searchParams.get("target");
  if (target) filtered = filtered.filter((r) => r.targets.includes(target));

  const q = url.searchParams.get("q")?.toLowerCase();
  if (q) {
    filtered = filtered.filter((r) =>
      `${r.purpose ?? ""} ${r.statement ?? ""} ${r.path ?? ""}`.toLowerCase().includes(q),
    );
  }
  if (url.searchParams.get("queue") === "promotable") {
    // The queue is the records that answered something and carry no decision
    // yet, most re-run first: the same rule the store applies.
    filtered = filtered
      .filter((r) => r.outcome === "satisfied" && !r.promoted_urn && !r.rejected_at)
      .sort((a, b) => b.reuse_count - a.reuse_count);
  }
  return filtered;
}

/** One page of a filtered list, in the envelope both surfaces use. */
function listPage(url: URL, records: CallRecord[]) {
  const page = parseInt(url.searchParams.get("page") ?? "1", 10);
  const perPage = parseInt(url.searchParams.get("per_page") ?? "25", 10);
  const filtered = applyFilters(url, records);
  const start = (page - 1) * perPage;

  return HttpResponse.json({
    data: filtered.slice(start, start + perPage),
    total: filtered.length,
    page,
    per_page: perPage,
  });
}

/** The record with this id, when the caller may read it. */
function findRecord(id: string, callerID?: string): CallRecord | undefined {
  const record = mockCallRecords.find((r) => r.id === id);
  if (!record) return undefined;
  // A record that is not the caller's own is not found, not forbidden: the
  // portal must not tell a reader that someone else's call exists.
  return callerID && record.user_id !== callerID ? undefined : record;
}

function notFound() {
  return HttpResponse.json(
    { title: "Not Found", status: 404, detail: "call record not found" },
    { status: 404 },
  );
}

/** The detail read, scoped the way the server scopes it. */
function detailOrNotFound(id: string, callerID?: string) {
  const record = findRecord(id, callerID);
  return record ? HttpResponse.json(record) : notFound();
}

/** Publishes a record, recording what it became on the fixture itself. */
function promote(id: string, actor: string, callerID?: string) {
  const record = findRecord(id, callerID);
  if (!record) return notFound();
  if (record.outcome !== "satisfied" || record.promoted_urn || record.rejected_at) {
    return HttpResponse.json(
      { title: "Conflict", status: 409, detail: "record is not promotable" },
      { status: 409 },
    );
  }
  record.promoted_urn =
    record.kind === "sql"
      ? `urn:li:query:${record.event_id}`
      : `example:${record.connection}:${record.operation_id}`;
  record.promoted_at = new Date().toISOString();
  record.promoted_by = actor;
  return HttpResponse.json(record);
}

/** Declines a record, with the note the reviewer gave. */
async function reject(id: string, actor: string, body: Request, callerID?: string) {
  const record = findRecord(id, callerID);
  if (!record) return notFound();
  const payload = (await body.json().catch(() => ({}))) as { note?: string };
  record.rejected_at = new Date().toISOString();
  record.rejected_by = actor;
  record.rejection_note = payload.note ?? "";
  return HttpResponse.json(record);
}

const ADMIN_ACTOR = "admin@example.com";

export const callHandlers = [
  http.get(`${ADMIN_BASE}/calls`, ({ request }) =>
    listPage(new URL(request.url), mockCallRecords),
  ),
  http.get(`${ADMIN_BASE}/calls/:id`, ({ params }) =>
    detailOrNotFound(String(params.id)),
  ),
  http.post(`${ADMIN_BASE}/calls/:id/promote`, ({ params }) =>
    promote(String(params.id), ADMIN_ACTOR),
  ),
  http.post(`${ADMIN_BASE}/calls/:id/reject`, ({ params, request }) =>
    reject(String(params.id), ADMIN_ACTOR, request),
  ),

  // The caller's own calls. Scoped here the way the server scopes them.
  http.get(`${PORTAL_BASE}/calls`, ({ request }) =>
    listPage(
      new URL(request.url),
      mockCallRecords.filter((r) => r.user_id === MOCK_CALLER_EMAIL),
    ),
  ),
  http.get(`${PORTAL_BASE}/calls/:id`, ({ params }) =>
    detailOrNotFound(String(params.id), MOCK_CALLER_EMAIL),
  ),
  http.post(`${PORTAL_BASE}/calls/:id/promote`, ({ params }) =>
    promote(String(params.id), MOCK_CALLER_EMAIL, MOCK_CALLER_EMAIL),
  ),
  http.post(`${PORTAL_BASE}/calls/:id/reject`, ({ params, request }) =>
    reject(String(params.id), MOCK_CALLER_EMAIL, request, MOCK_CALLER_EMAIL),
  ),
];
