import { http, HttpResponse } from "msw";
import type {
  PendingReview,
  ScriptApproveInput,
  ScriptVersion,
} from "@/api/admin/types";
import {
  mockScriptReviewAlert,
  mockScriptReviewPayloads,
  mockScriptReviews,
  mockScriptVersions,
  mockScripts,
} from "../data/scripts";

const ADMIN_BASE = "/api/v1/admin";

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
