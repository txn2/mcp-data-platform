import { http, HttpResponse } from "msw";
import type { ApprovalKind, ApprovalRequest, RecentCall } from "@/api/portal/hooks/approvals";

// The Approvals queue (#2437, #2494) and a form's preview: module state, so a
// request filed or decided within one mock session is what the page reads.
//   GET  /portal/approvals, POST /portal/approvals
//   GET  /portal/approvals/:id, POST /portal/approvals/:id/decide
//   GET  /portal/approvals/:id/calls
//   POST /portal/scripts/:id/form-preview

const BASE = "/api/v1/portal/approvals";

const requests: ApprovalRequest[] = [
  {
    id: "apr_intake2",
    subject_kind: "script_version",
    purpose: "form_public",
    script_id: "script-intake",
    script_name: "order-intake",
    version: 2,
    source_sha256: "9f2c4d8e1b7a",
    requester_email: "j.martinez@example.com",
    requester_persona: "analyst",
    reason: "The intake form goes on the public site next week.",
    changes: {
      approved_version: 1,
      diff: "--- version 1 (approved)\n+++ version 2\n@@ -3,3 +3,4 @@\n     return {\n         \"fields\": [\n             {\"name\": \"email\", \"type\": \"email\", \"required\": True},\n+            {\"name\": \"company\", \"type\": \"text\"},\n",
      added: 1,
      removed: 0,
    },
    status: "pending",
    self_approved: false,
    created_at: "2026-11-05T09:30:00Z",
  },
  {
    id: "apr_intake1",
    subject_kind: "script_version",
    purpose: "form_public",
    script_id: "script-intake",
    script_name: "order-intake",
    version: 1,
    source_sha256: "4a1b9c3d2e0f",
    requester_email: "j.martinez@example.com",
    changes: { approved_version: 0, added: 0, removed: 0 },
    status: "approved",
    decided_by: "admin@example.com",
    decided_at: "2026-10-28T15:00:00Z",
    decision_comment: "Fields and confirmation read well.",
    self_approved: false,
    created_at: "2026-10-28T14:00:00Z",
  },
  {
    id: "apr_call_subscribers",
    subject_kind: "call",
    purpose: "call_allowance",
    kind: "call",
    script_id: "",
    version: 0,
    source_sha256: "",
    requester_email: "jo@example.com",
    requester_persona: "marketing",
    reason: "Weekly newsletter import for the Nov 14 send.",
    changes: { approved_version: 0, added: 0, removed: 0 },
    status: "pending",
    self_approved: false,
    created_at: "2026-11-13T15:41:00Z",
    tool: "api_invoke_endpoint",
    connection: "billing-api",
    operation: "addSubscribers",
    rendering:
      "POST /lists/newsletter-weekly/subscribers     5 items\nheaders  X-Api-Key: {{secret:billing-key}}\nbody     { \"subscribers\": [ { \"email\": \"a@example.com\" }, … 4 more ] }",
    handlers: [{ id: "ch_ratelimit", script: "subscriber-rate-limit", summary: "5 subscribers in one call; the limit is 3 per hour, 0 used." }],
    approvers: ["ops-leads"],
    calls_asked: 1,
  },
  {
    id: "apr_call_leads",
    subject_kind: "call",
    purpose: "call_allowance",
    kind: "call",
    script_id: "",
    version: 0,
    source_sha256: "",
    requester_email: "sarah.chen@example.com",
    changes: { approved_version: 0, added: 0, removed: 0 },
    status: "approved",
    decided_by: "ana@example.com",
    decided_at: "2026-11-13T10:00:00Z",
    self_approved: false,
    created_at: "2026-11-13T09:40:00Z",
    tool: "api_invoke_endpoint",
    connection: "crm",
    operation: "createLead",
    rendering: "POST /leads     1 item",
    approvers: [],
    calls_asked: 10,
    calls_approved: 10,
    calls_used: 3,
    until: "2026-11-15T17:00:00Z",
  },
  {
    id: "apr_handler_paying",
    subject_kind: "script_version",
    purpose: "call_handler",
    kind: "handler_version",
    script_id: "script-paying",
    script_name: "paying-customer-check",
    version: 5,
    source_sha256: "7c1d",
    requester_email: "kim@example.com",
    changes: {
      approved_version: 4,
      diff: "@@ changes since version 4 @@\n-  if acct[\"plan\"] == \"free\":\n+  if acct[\"plan\"] in (\"free\", \"trial\"):\n",
      added: 1,
      removed: 1,
    },
    status: "pending",
    self_approved: false,
    created_at: "2026-11-13T14:10:00Z",
  },
];

// recentCalls are a held call's requester's calls of its operation.
const recentCalls: Record<string, RecentCall[]> = {
  apr_call_subscribers: [
    { at: "2026-11-13T15:41:00Z", items: 5, outcome: "held" },
    { at: "2026-11-13T11:02:00Z", items: 1, outcome: "ran" },
    { at: "2026-11-12T16:20:00Z", items: 2, outcome: "ran" },
  ],
};

// kindOf is the type a request is listed under, as the server reads it.
function kindOf(r: ApprovalRequest): ApprovalKind {
  if (r.subject_kind === "call") return "call";
  if (r.purpose === "call_handler") return "handler_version";
  if (r.purpose === "form_public") return "form";
  return "site";
}

function problem(status: number, detail: string) {
  return HttpResponse.json({ status, detail }, { status });
}

export const approvalHandlers = [
  http.get(BASE, ({ request }) => {
    const url = new URL(request.url);
    const status = url.searchParams.get("status");
    const script = url.searchParams.get("script_id");
    const kind = url.searchParams.get("kind");
    const out = requests.filter(
      (r) => (!status || r.status === status) && (!script || r.script_id === script) && (!kind || kindOf(r) === kind),
    );
    return HttpResponse.json({ requests: out, count: out.length });
  }),

  http.post(BASE, async ({ request }) => {
    const body = (await request.json()) as { script?: string; version?: number; reason?: string };
    if (!body.script) return problem(422, "name the script: its id, or the name of one of your scripts");
    const r: ApprovalRequest = {
      id: `apr_${requests.length + 1}`,
      subject_kind: "script_version",
      purpose: "form_public",
      script_id: body.script,
      version: body.version || 3,
      source_sha256: "0000",
      requester_email: "sarah.chen@example.com",
      reason: body.reason,
      changes: { approved_version: 0, added: 0, removed: 0 },
      status: "pending",
      self_approved: false,
      created_at: new Date().toISOString(),
    };
    requests.unshift(r);
    return HttpResponse.json(r);
  }),

  http.get(`${BASE}/:id`, ({ params }) => {
    const r = requests.find((x) => x.id === params.id);
    return r ? HttpResponse.json(r) : problem(404, "no such request");
  }),

  http.post(`${BASE}/:id/decide`, async ({ params, request }) => {
    const r = requests.find((x) => x.id === params.id);
    if (!r) return problem(404, "no such request");
    if (r.status !== "pending") return problem(409, "the request was already decided");
    const body = (await request.json()) as { decision?: string; comment?: string; calls?: number; until?: string };
    if (r.subject_kind === "call" && body.decision === "approve") {
      if ((body.calls ?? 0) > (r.calls_asked ?? 1)) {
        return problem(422, `${r.calls_asked} calls were asked for; an approval grants at most that many`);
      }
      r.calls_approved = body.calls || r.calls_asked;
      r.calls_used = 0;
      r.until = body.until ?? new Date(Date.now() + 24 * 3600 * 1000).toISOString();
      r.arguments = undefined;
    }
    r.status = body.decision === "approve" ? "approved" : "denied";
    r.decided_by = "admin@example.com";
    r.decided_at = new Date().toISOString();
    r.decision_comment = body.comment;
    return HttpResponse.json(r);
  }),

  http.get(`${BASE}/:id/calls`, ({ params }) => {
    const r = requests.find((x) => x.id === params.id);
    if (!r) return problem(404, "no such request");
    return HttpResponse.json({ data: recentCalls[r.id] ?? [] });
  }),

  http.post("/api/v1/portal/scripts/:id/form-preview", () =>
    HttpResponse.json({
      id: "fp0123",
      url: "/forms/_preview/fp0123",
      expires_at: new Date(Date.now() + 30 * 60 * 1000).toISOString(),
    }),
  ),
];
