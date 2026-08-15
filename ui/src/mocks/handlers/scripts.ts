import { http, HttpResponse } from "msw";
import type {
  PendingReview,
  ScriptApproveInput,
  ScriptVersion,
} from "@/api/admin/types";
import {
  mockScriptContracts,
  mockScriptReviewAlert,
  mockScriptReviewPayloads,
  mockScriptReviews,
  mockScriptRunDetails,
  mockScriptRuns,
  mockScriptSchedules,
  mockScriptVersions,
  mockScripts,
} from "../data/scripts";

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";

// Mutable copies so approving and rejecting move a row out of the queue within
// one mock-server session, which is what the demo and the screenshots read.
const scripts = JSON.parse(JSON.stringify(mockScripts)) as typeof mockScripts;
const reviews = JSON.parse(JSON.stringify(mockScriptReviews)) as PendingReview[];
const versions = JSON.parse(JSON.stringify(mockScriptVersions)) as Record<
  string,
  ScriptVersion[]
>;
const alert = JSON.parse(JSON.stringify(mockScriptReviewAlert));

// resolveDecision removes the decided version from the queue, which is what the
// server's queue predicate does once a version is no longer pending.
function resolveDecision(scriptID: string, version: number) {
  const at = reviews.findIndex((r) => r.script_id === scriptID && r.version === version);
  if (at >= 0) reviews.splice(at, 1);
}

// emptyDemoRequested reports whether the page URL asks this surface to answer
// as though the caller had nothing: ?empty=scripts. It is a fixture control, so
// it lives here in the mocks and nothing in the application reads it.
function emptyDemoRequested(surface: string): boolean {
  if (typeof window === "undefined") return false;
  return new URLSearchParams(window.location.search).get("empty") === surface;
}

// setScheduleEnabled pauses or resumes a schedule, keeping the contract the
// detail page reads in step with it. Pausing never touches the cadence, which
// is why it is its own route rather than a field of one.
function setScheduleEnabled(scriptID: string, enabled: boolean) {
  const sched = mockScriptSchedules[scriptID];
  if (!sched) {
    return HttpResponse.json({ detail: "this script has no schedule" }, { status: 404 });
  }
  sched.enabled = enabled;
  const contract = mockScriptContracts[scriptID];
  if (contract?.schedule) {
    contract.schedule.enabled = enabled;
  }
  return HttpResponse.json(sched);
}

// alertWarnings mirrors the server's check for a configuration that saves
// cleanly and delivers nothing.
function alertWarnings(): string[] {
  if (!alert.enabled) return [];
  const out: string[] = [];
  if (alert.recipients.length === 0) {
    out.push(
      "no recipients are configured, so no alert will be delivered; add at least one address",
    );
  }
  if (alert.pending_threshold <= 0 && alert.oldest_pending_days <= 0) {
    out.push(
      "both thresholds are 0, so nothing can cross; set a pending count, an age in days, or both",
    );
  }
  return out;
}

// Managed-script review handlers (#1287), mirroring
// internal/httpserver/scripthttp.
export const scriptHandlers = [
  http.get(`${ADMIN_BASE}/scripts/reviews`, () =>
    HttpResponse.json({ data: reviews, total: reviews.length }),
  ),

  http.get(`${ADMIN_BASE}/scripts`, () =>
    HttpResponse.json({ data: scripts, total: scripts.length }),
  ),

  http.get(`${ADMIN_BASE}/scripts/:id/versions`, ({ params }) => {
    const list = versions[String(params.id)] ?? [];
    return HttpResponse.json({ data: list, total: list.length });
  }),

  http.get(`${ADMIN_BASE}/scripts/:id/versions/:version`, ({ params }) => {
    const payload = mockScriptReviewPayloads[`${params.id}/${params.version}`];
    if (!payload) {
      return HttpResponse.json({ detail: "version not found" }, { status: 404 });
    }
    return HttpResponse.json(payload);
  }),

  http.post(`${ADMIN_BASE}/scripts/:id/versions/:version/approve`, async ({ params, request }) => {
    const body = (await request.json()) as ScriptApproveInput;
    const scriptID = String(params.id);
    const version = Number(params.version);
    const list = versions[scriptID] ?? [];
    const target = list.find((v) => v.version === version);
    if (!target) {
      return HttpResponse.json({ detail: "version not found" }, { status: 404 });
    }
    // The server refuses a grant the code would outrun, so the mock does too:
    // approving into a grant that covers nothing is the error state the
    // surface has to be able to show.
    if (!(body.capabilities ?? []).length) {
      return HttpResponse.json(
        {
          detail:
            "the grant does not cover capabilities this version calls: platform.query, platform.export",
        },
        { status: 400 },
      );
    }
    target.status = "applied";
    target.approved_by = "sarah.chen@example.com";
    target.approved_at = new Date().toISOString();
    // Roles come from the version's author, never from the request.
    target.grants = {
      roles: target.author_roles ?? [],
      connections: body.connections ?? [],
      capabilities: body.capabilities ?? [],
      destinations: body.destinations ?? [],
    };
    const script = scripts.find((s) => s.id === scriptID);
    if (script) {
      script.approved_version_id = target.id;
      script.version = target.version;
      script.status = script.status === "draft" ? "active" : script.status;
    }
    resolveDecision(scriptID, version);
    return HttpResponse.json(target);
  }),

  http.post(`${ADMIN_BASE}/scripts/:id/versions/:version/reject`, ({ params }) => {
    const scriptID = String(params.id);
    const version = Number(params.version);
    const target = (versions[scriptID] ?? []).find((v) => v.version === version);
    if (!target || target.status !== "draft") {
      return HttpResponse.json(
        { detail: `version ${version} is not a pending draft, so there is nothing to reject` },
        { status: 409 },
      );
    }
    target.status = "rejected";
    resolveDecision(scriptID, version);
    return HttpResponse.json({ status: "rejected" });
  }),

  http.get(`${ADMIN_BASE}/settings/script-review-alert`, () => {
    alert.warnings = alertWarnings();
    return HttpResponse.json(alert);
  }),

  // Portal script pages (#1290). The mock caller is an administrator, so every
  // script comes back owned: that is what the server answers an admin, whose
  // reach into this surface is unrestricted by design.
  http.get(`${PORTAL_BASE}/scripts`, () => {
    // An account with no automations is a real product state, and the fixture
    // set is deliberately not empty. The demo and the screenshots reach it by
    // asking for it in the page URL: the handlers run in the page and can see
    // it, and the app itself ignores query parameters it does not know.
    if (emptyDemoRequested("scripts")) {
      return HttpResponse.json({ data: [], total: 0 });
    }
    const data = scripts.map((script) => {
      const runs = mockScriptRuns[script.id] ?? [];
      return {
        script,
        schedule: mockScriptSchedules[script.id],
        last_run: runs[0],
        owned: true,
      };
    });
    return HttpResponse.json({ data, total: data.length });
  }),

  http.get(`${PORTAL_BASE}/scripts/:id`, ({ params }) => {
    const contract = mockScriptContracts[String(params.id)];
    if (!contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    return HttpResponse.json({ contract, owned: true });
  }),

  http.get(`${PORTAL_BASE}/scripts/:id/versions`, ({ params }) => {
    const list = versions[String(params.id)] ?? [];
    return HttpResponse.json({ data: list, total: list.length });
  }),

  http.get(`${PORTAL_BASE}/scripts/:id/runs`, ({ params }) => {
    const list = mockScriptRuns[String(params.id)] ?? [];
    return HttpResponse.json({ data: list, total: list.length });
  }),

  http.get(`${PORTAL_BASE}/scripts/:id/runs/:runID`, ({ params }) => {
    const run = mockScriptRunDetails[String(params.runID)];
    // A run of another script is answered exactly as a run that does not
    // exist, which is what the server does with it.
    if (!run || run.script_id !== String(params.id)) {
      return HttpResponse.json({ detail: "run not found" }, { status: 404 });
    }
    return HttpResponse.json(run);
  }),

  // The cadence is the owner's to set, and the only mutation the portal's
  // script surface has (#1307).
  http.put(`${PORTAL_BASE}/scripts/:id/schedule`, async ({ params, request }) => {
    const body = (await request.json()) as { cron?: string; timezone?: string };
    const id = String(params.id);
    if (!body.cron || !/^(@\w+|[\d*/,\- ]+)$/.test(body.cron)) {
      return HttpResponse.json(
        { detail: `unparseable cron expression: ${body.cron ?? ""}` },
        { status: 400 },
      );
    }
    const existing = mockScriptSchedules[id];
    const sched = {
      id: existing?.id ?? `sched-${id}`,
      script_id: id,
      cron_spec: body.cron,
      timezone: body.timezone || "UTC",
      enabled: existing?.enabled ?? true,
      next_run_at: existing?.next_run_at,
    };
    mockScriptSchedules[id] = sched;
    const contract = mockScriptContracts[id];
    if (contract) {
      contract.schedule = {
        cron_spec: sched.cron_spec,
        timezone: sched.timezone,
        enabled: sched.enabled,
        next_run_at: sched.next_run_at,
      };
    }
    return HttpResponse.json(sched);
  }),

  http.post(`${PORTAL_BASE}/scripts/:id/schedule/enable`, ({ params }) =>
    setScheduleEnabled(String(params.id), true),
  ),

  http.post(`${PORTAL_BASE}/scripts/:id/schedule/disable`, ({ params }) =>
    setScheduleEnabled(String(params.id), false),
  ),

  http.put(`${ADMIN_BASE}/settings/script-review-alert`, async ({ request }) => {
    const body = (await request.json()) as Record<string, unknown>;
    alert.enabled = Boolean(body.enabled);
    alert.pending_threshold = Number(body.pending_threshold ?? 0);
    alert.oldest_pending_days = Number(body.oldest_pending_days ?? 0);
    alert.cooldown_hours = Number(body.cooldown_hours ?? 24);
    alert.recipients = (Array.isArray(body.recipients) ? body.recipients : []).map((r) =>
      String(r).trim().toLowerCase(),
    );
    alert.updated_by = "sarah.chen@example.com";
    alert.updated_at = new Date().toISOString();
    alert.warnings = alertWarnings();
    return HttpResponse.json(alert);
  }),
];
