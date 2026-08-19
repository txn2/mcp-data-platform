import { http, HttpResponse } from "msw";
import type {
  PendingReview,
  ScriptApproveInput,
  ScriptVersion,
} from "@/api/admin/types";
import type { ScriptSchedule } from "@/api/portal/hooks/scripts";
import {
  mockReachableConnections,
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

// referencedIn is the mock's stand-in for the static read the server performs:
// the names of `candidates` that appear literally in the source. It is enough
// for a demo and a screenshot, and it is deliberately not a parser.
function referencedIn(source: string, candidates: string[]): string[] {
  return candidates.filter((name) => source.includes(name));
}

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
// Schedules are mutable for the same reason: a cadence set on this surface has
// to be the cadence the page reads back.
const schedules = JSON.parse(JSON.stringify(mockScriptSchedules)) as Record<
  string,
  ScriptSchedule
>;

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

// setScheduleEnabled is the pause path both routes share. A script with no
// schedule has nothing to pause, which is a 404 on the server too.
function setScheduleEnabled(scriptID: string, enabled: boolean) {
  const schedule = schedules[scriptID];
  if (!schedule) {
    return HttpResponse.json({ detail: "this script has no schedule" }, { status: 404 });
  }
  schedule.enabled = enabled;
  return HttpResponse.json(reportable(schedule));
}

// scheduleOf is a script's cadence as the listing may report it, absent when
// the script runs on demand.
function scheduleOf(scriptID: string): ScriptSchedule | undefined {
  const schedule = schedules[scriptID];
  return schedule ? reportable(schedule) : undefined;
}

// reportable drops the one field the server withholds from a paused schedule.
// The stored due time is the fire it resumes on, so the fixture keeps it and
// the payload does not, which is what the server does with the same row.
function reportable(schedule: ScriptSchedule): ScriptSchedule {
  if (schedule.enabled) return schedule;
  const { next_run_at: _parked, ...rest } = schedule;
  return rest;
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

  // The operator's cross-script run listing (#1307).
  http.get(`${ADMIN_BASE}/scripts/runs`, () => {
    const all = Object.entries(mockScriptRuns).flatMap(([scriptID, runs]) =>
      runs.map((run) => ({ ...run, script_id: scriptID })),
    );
    all.sort((a, b) => (a.fire_time < b.fire_time ? 1 : -1));
    // The server answers with the cap it read under, which is what lets the
    // page say there is older history behind a full page.
    return HttpResponse.json({ data: all, total: all.length, limit: 50 });
  }),

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
        // The mutable map, not the fixture: a cadence saved or paused in this
        // session has to be what the listing shows next, and a paused schedule
        // withholds its next fire here exactly as it does on its own route.
        schedule: scheduleOf(script.id),
        last_run: runs[0],
        owned: true,
      };
    });
    return HttpResponse.json({ data, total: data.length });
  }),

  http.get(`${PORTAL_BASE}/scripts/:id`, ({ params }) => {
    const id = String(params.id);
    const contract = mockScriptContracts[id];
    if (!contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    // The live source and the contract that code was written against travel
    // with the document for the owner: the editor opens the one and the dry-run
    // form binds the other. In these fixtures the live and approved parameter
    // contracts agree, which is the ordinary case.
    const live = (versions[id] ?? []).find((v) => v.status === "applied");
    return HttpResponse.json({
      contract,
      owned: true,
      source: live?.source ?? "",
      draft_params: contract.params,
    });
  }),

  // Editing the code (#1307). An approved script's edit becomes a draft the
  // review queue then holds, which is the server's rule and the reason the
  // page reports an outcome rather than saying "saved".
  http.put(`${PORTAL_BASE}/scripts/:id/source`, async ({ params, request }) => {
    const id = String(params.id);
    const body = (await request.json()) as { source?: string };
    if (!body.source?.trim()) {
      return HttpResponse.json(
        { detail: "the source does not parse, so it was not saved" },
        { status: 400 },
      );
    }
    const list = versions[id] ?? [];
    const script = scripts.find((s) => s.id === id);
    const next = Math.max(0, ...list.map((v) => v.version)) + 1;
    // A personal script its own owner edits is approved by the save itself
    // (#1367): the platform mints the grant from what the source reaches, and
    // no reviewer is asked — so this is answered BEFORE the review branch,
    // which is what an approved shared script's edit takes. The demo caller is
    // sarah.chen, so the fixture's personal script is hers.
    if (script?.scope === "personal" && script.owner_email === "sarah.chen@example.com") {
      if (list[0]) {
        list[0].source = body.source;
        list[0].auto_approved = true;
      }
      return HttpResponse.json({
        applied: true,
        approved: true,
        message:
          "Saved and approved. This script is yours alone, so the platform approved this version " +
          "for you and runs it under the access you hold. It runs now, and on its schedule.",
      });
    }
    if (script?.approved_version_id) {
      list.unshift({
        ...list[0]!,
        id: `${id}-v${next}`,
        version: next,
        source: body.source,
        status: "draft",
        approved_by: "",
        approved_at: undefined,
        created_at: new Date().toISOString(),
      });
      versions[id] = list;
      reviews.unshift({
        script_id: id,
        script_name: script.name,
        display_name: script.display_name,
        description: script.description,
        version: next,
        author: "sarah.chen@example.com",
        author_roles: script.version ? ["analyst"] : [],
        created_at: new Date().toISOString(),
        first_approval: false,
      } as PendingReview);
      return HttpResponse.json({
        applied: false,
        pending_version: next,
        message:
          "This script has an approved version, so the change was saved as a draft awaiting review. " +
          "The approved version keeps running until the draft is approved.",
      });
    }
    if (list[0]) list[0].source = body.source;
    return HttpResponse.json({
      applied: true,
      message: "Saved. Nothing is approved for this script yet, so nothing executes it unattended.",
    });
  }),

  // The owner's exercise loop (#1361, #1363, #1364): the connections a
  // parameter may name, a run of the approved version, and the two checks an
  // author makes before asking anybody to approve an edit.

  // The set a connection parameter chooses from. Which set depends on what will
  // execute: an approved run is confined to the grant, while a dry run reaches
  // what its caller reaches, and answering with the wrong one would offer values
  // the run then refuses.
  http.get(`${PORTAL_BASE}/scripts/:id/connections`, ({ params, request }) => {
    const id = String(params.id);
    if (!mockScriptContracts[id]) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    if (new URL(request.url).searchParams.get("audience") === "draft") {
      return HttpResponse.json({
        data: mockReachableConnections,
        source: "persona",
        note:
          "A dry run executes as you, so it reaches the connections you reach. " +
          "An approved run is confined to what its approval granted instead.",
      });
    }
    // The approved version is the one the execution gate points at, resolved by
    // id rather than by status: an approved version's status is "applied", so a
    // status test would find none and offer an empty set.
    const gate = scripts.find((sc) => sc.id === id)?.approved_version_id;
    const granted = (versions[id] ?? []).find((v) => v.id === gate)?.grants?.connections;
    const data = mockReachableConnections.filter((c) => (granted ?? []).includes(c.name));
    return HttpResponse.json({
      data,
      source: "grant",
      note: data.length
        ? "These are the connections this script's approved version may reach. " +
          "A run naming any other is refused."
        : "Nothing is approved for this script, or its approval granted no connection, " +
          "so a run of it can name none.",
    });
  }),

  // Running the approved version now. It queues what run_script queues; the
  // response carries the run id and never a result, because a worker executes
  // it and the history is where it is followed.
  http.post(`${PORTAL_BASE}/scripts/:id/runs`, async ({ params, request }) => {
    const id = String(params.id);
    const contract = mockScriptContracts[id];
    if (!contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    if (contract.approval.refusal) {
      return HttpResponse.json({ detail: contract.approval.refusal }, { status: 400 });
    }
    await request.json();
    return HttpResponse.json(
      {
        run_id: `dpx_${Date.now().toString(36)}`,
        status: "pending",
        version: contract.approval.version ?? 0,
        message: "Queued. It appears in this script's run history and updates as it progresses.",
      },
      { status: 202 },
    );
  }),

  // Validating an edit. It parses and reports; it executes nothing and stores
  // nothing, so the mock answers from the source it was sent.
  http.post(`${PORTAL_BASE}/scripts/:id/validate`, async ({ request }) => {
    const body = (await request.json()) as { source?: string };
    const source = body.source ?? "";
    if (!source.trim() || source.includes("while ")) {
      return HttpResponse.json({
        ok: false,
        findings: [
          {
            severity: "error",
            line: 1,
            message: "while loops are not available in this dialect",
            hint: "Loop over a list, or express the repetition in SQL.",
          },
        ],
        capabilities: [],
        connections: [],
        destinations: [],
        dynamic_connections: false,
        dynamic_destinations: false,
      });
    }
    return HttpResponse.json({
      ok: true,
      findings: [],
      capabilities: referencedIn(source, ["platform.query", "platform.export"]),
      connections: referencedIn(source, mockReachableConnections.map((c) => c.name)),
      destinations: source.includes("platform.export") ? ["portal"] : [],
      dynamic_connections: false,
      dynamic_destinations: false,
    });
  }),

  // Dry-running an edit. Nothing is persisted, which is why the outputs carry a
  // shape and no locator.
  http.post(`${PORTAL_BASE}/scripts/:id/dry-run`, async ({ request }) => {
    const body = (await request.json()) as { source?: string };
    const source = body.source ?? "";
    if (source.includes("fail(")) {
      return HttpResponse.json({
        run_id: "dpx_draft_demo",
        status: "failed",
        error: "script failed: deliberate stop\n  at line 4",
        log: "reading yesterday's rows",
        metrics: { steps: 128, duration_ms: 210, queries: 1, exports: 0 },
        outputs: [],
        message:
          "A script failure is deterministic: the same source on the same inputs fails the " +
          "same way, so running it again changes nothing. Fix the script and dry-run it again.",
      });
    }
    return HttpResponse.json({
      run_id: "dpx_draft_demo",
      status: "succeeded",
      log: "reading yesterday's rows\n1,284 rows for 2026-08-17",
      metrics: { steps: 1042, duration_ms: 1830, queries: 1, exports: 1 },
      outputs: [
        { name: "daily_sales", destination: "portal", format: "csv", row_count: 1284, bytes: 48213 },
      ],
      message:
        "Nothing was persisted. platform.export reported the shape of each output " +
        "rather than writing it.",
    });
  }),

  http.get(`${PORTAL_BASE}/scripts/:id/versions`, ({ params }) => {
    const list = versions[String(params.id)] ?? [];
    return HttpResponse.json({ data: list, total: list.length });
  }),

  // The owner's cadence controls (#1307). They mutate the fixture in place, so
  // saving a cadence and then pausing it behaves as it does against the server
  // within one mock-server session.
  http.get(`${PORTAL_BASE}/scripts/:id/schedule`, ({ params }) => {
    const schedule = schedules[String(params.id)];
    if (!schedule) {
      return HttpResponse.json({ detail: "this script has no schedule" }, { status: 404 });
    }
    return HttpResponse.json(reportable(schedule));
  }),

  http.put(`${PORTAL_BASE}/scripts/:id/schedule`, async ({ params, request }) => {
    const id = String(params.id);
    const body = (await request.json()) as {
      cron?: string;
      timezone?: string;
      params?: Record<string, unknown>;
    };
    // The server refuses a cadence it cannot parse before anything is stored,
    // and naming what to fix is the whole of that answer.
    if (!body.cron?.trim()) {
      return HttpResponse.json(
        { detail: 'a schedule needs a cron expression, for example "0 7 * * 1-5" for 07:00 on weekdays' },
        { status: 400 },
      );
    }
    const previous = schedules[id];
    schedules[id] = {
      id: previous?.id ?? `sched-${id}`,
      script_id: id,
      cron_spec: body.cron.trim(),
      timezone: body.timezone?.trim() || "UTC",
      params: body.params,
      // Replacing a cadence keeps the paused state: editing a parked
      // automation must not quietly restart it.
      enabled: previous?.enabled ?? true,
      // The next fire is recomputed from now whether or not the schedule is
      // enabled, as the server does: the old cadence's next fire is not a fire
      // this schedule has any more. A paused schedule simply does not report it.
      next_run_at: new Date(Date.now() + 22 * 3_600_000).toISOString(),
      last_fire_at: previous?.last_fire_at,
      missed_fires: previous?.missed_fires ?? 0,
    };
    return HttpResponse.json(reportable(schedules[id]));
  }),

  // Pausing and resuming are two routes rather than one with the state in the
  // path, exactly as the server registers them.
  http.post(`${PORTAL_BASE}/scripts/:id/schedule/enable`, ({ params }) =>
    setScheduleEnabled(String(params.id), true),
  ),

  http.post(`${PORTAL_BASE}/scripts/:id/schedule/disable`, ({ params }) =>
    setScheduleEnabled(String(params.id), false),
  ),

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
