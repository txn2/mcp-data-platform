import { http, HttpResponse } from "msw";
import type { ApprovalRequest } from "@/api/portal/hooks/approvals";

// The Approvals queue (#2437, #2494) and a form's preview: module state, so a
// request filed or decided within one mock session is what the page reads.
//   GET  /portal/approvals, POST /portal/approvals
//   GET  /portal/approvals/:id, POST /portal/approvals/:id/decide
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
];

function problem(status: number, detail: string) {
  return HttpResponse.json({ status, detail }, { status });
}

export const approvalHandlers = [
  http.get(BASE, ({ request }) => {
    const url = new URL(request.url);
    const status = url.searchParams.get("status");
    const script = url.searchParams.get("script_id");
    const out = requests.filter((r) => (!status || r.status === status) && (!script || r.script_id === script));
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
    const body = (await request.json()) as { decision?: string; comment?: string };
    r.status = body.decision === "approve" ? "approved" : "denied";
    r.decided_by = "admin@example.com";
    r.decided_at = new Date().toISOString();
    r.decision_comment = body.comment;
    return HttpResponse.json(r);
  }),

  http.post("/api/v1/portal/scripts/:id/form-preview", () =>
    HttpResponse.json({
      id: "fp0123",
      url: "/forms/_preview/fp0123",
      expires_at: new Date(Date.now() + 30 * 60 * 1000).toISOString(),
    }),
  ),
];
