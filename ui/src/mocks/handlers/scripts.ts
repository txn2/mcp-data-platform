import { http, HttpResponse } from "msw";
import type { ScriptVersion } from "@/api/admin/types";
import type { ScriptSchedule } from "@/api/portal/hooks/scripts";
import {
  mockBindableConnections,
  mockConnectionNames,
  mockScriptContracts,
  mockScriptRunDetails,
  mockScriptRuns,
  mockScriptSchedules,
  mockScriptVersionDetails,
  mockScriptVersions,
  mockScripts,
} from "../data/scripts";

// referencedIn is the mock's stand-in for the static read the server performs:
// the names of `candidates` that appear literally in the source. It is enough
// for a demo and a screenshot, and it is deliberately not a parser.
function referencedIn(source: string, candidates: string[]): string[] {
  return candidates.filter((name) => source.includes(name));
}

// calledTools is the mock's stand-in for the tool names the server reads out of
// platform.call. Like referencedIn it is a demo aid, not a parser.
const CALL_TOOL_RE = /platform\.call\(\s*(?:tool\s*=\s*)?["']([^"']+)["']/g;
function calledTools(source: string): string[] {
  const names = new Set<string>();
  for (const match of source.matchAll(CALL_TOOL_RE)) {
    if (match[1]) names.add(match[1]);
  }
  return [...names].sort();
}

const ADMIN_BASE = "/api/v1/admin";
const PORTAL_BASE = "/api/v1/portal";

// Mutable copies so saving an edit within one mock-server session is what the
// page reads back, which is what the demo and the screenshots read.
const scripts = JSON.parse(JSON.stringify(mockScripts)) as typeof mockScripts;
const versions = JSON.parse(JSON.stringify(mockScriptVersions)) as Record<
  string,
  ScriptVersion[]
>;
// Contracts are mutable for the same reason the records are: documenting a
// script on its own page has to be what the page reads back (#1369), and a
// saved edit has to be the version the contract says runs.
const contracts = JSON.parse(JSON.stringify(mockScriptContracts)) as typeof mockScriptContracts;

// LONG_DESCRIPTION_BYTES mirrors the server's advisory threshold: a description
// at or over it gets a non-blocking suggestion that it might belong somewhere
// of its own. Nothing is ever refused for it.
const LONG_DESCRIPTION_BYTES = 16 * 1024;

// descriptionNotice is the mock of script.DescriptionNotice, so the form's
// advisory branch is exercised by the same input the server would advise on.
function descriptionNotice(description: string): string | undefined {
  if (description.length < LONG_DESCRIPTION_BYTES) return undefined;
  return (
    `this description is ${description.length} bytes, which is long enough to be a document ` +
    "in its own right; consider moving the background to a knowledge page and leaving the " +
    "description to what this script does, takes and produces"
  );
}
// Schedules are mutable for the same reason: a cadence set on this surface has
// to be the cadence the page reads back.
const schedules = JSON.parse(JSON.stringify(mockScriptSchedules)) as Record<
  string,
  ScriptSchedule
>;

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

// Managed-script handlers, mirroring internal/httpserver/scripthttp.
export const scriptHandlers = [
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

  // One version in full: the snapshot, what a static read of its source found,
  // and the account of somebody having executed this exact source, when one
  // exists (#1364).
  http.get(`${ADMIN_BASE}/scripts/:id/versions/:version`, ({ params }) => {
    const payload = mockScriptVersionDetails[`${params.id}/${params.version}`];
    if (!payload) {
      return HttpResponse.json({ detail: "version not found" }, { status: 404 });
    }
    return HttpResponse.json(payload);
  }),

  // Portal script pages (#1290). The mock caller is an administrator, so every
  // script comes back owned: that is what the server answers an admin, whose
  // reach into this surface is unrestricted by design.
  http.get(`${PORTAL_BASE}/scripts`, ({ request }) => {
    // An account with no automations is a real product state, and the fixture
    // set is deliberately not empty. The demo and the screenshots reach it by
    // asking for it in the page URL: the handlers run in the page and can see
    // it, and the app itself ignores query parameters it does not know.
    if (emptyDemoRequested("scripts")) {
      return HttpResponse.json({ data: [], total: 0 });
    }
    // The category and tag axes narrow the listing on the server (#1369), so
    // the mock narrows it too: a page that filtered its own rows would pass
    // against a server that ignored the query.
    const query = new URL(request.url).searchParams;
    const category = query.get("category");
    const tags = query.getAll("tag");
    // The free text is a store predicate too (#1405), over the same three
    // fields the store matches: what the script is called, what it is called
    // on a page, and what it says about itself.
    const search = (query.get("search") ?? "").toLowerCase();
    const data = scripts
      .filter((script) => !category || script.category === category)
      .filter((script) => tags.length === 0 || tags.some((t) => (script.tags ?? []).includes(t)))
      .filter(
        (script) =>
          !search ||
          [script.name, script.display_name, script.description]
            .filter((field): field is string => !!field)
            .some((field) => field.toLowerCase().includes(search)),
      )
      .map((script) => {
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

  // The caller's runs across every script they own (#1405). The mock caller is
  // an administrator, so this is every run the fixture holds, which is what the
  // server answers them.
  http.get(`${PORTAL_BASE}/scripts/runs`, ({ request }) => {
    if (emptyDemoRequested("scripts")) {
      return HttpResponse.json({ data: [], total: 0, limit: 50 });
    }
    const named = new Map(scripts.map((script) => [script.id, script.display_name || script.name]));
    // The listing narrows to one script on the server (#1407), so the mock
    // narrows it too: a page that filtered its own rows would pass against a
    // server that ignored the parameter.
    const only = new URL(request.url).searchParams.get("script_id");
    const all = Object.entries(mockScriptRuns)
      .filter(([scriptID]) => !only || scriptID === only)
      .flatMap(([scriptID, runs]) =>
        runs.map((run) => ({
          ...run,
          script_id: scriptID,
          script_name: named.get(scriptID),
        })),
      );
    all.sort((a, b) => (a.fire_time < b.fire_time ? 1 : -1));
    return HttpResponse.json({ data: all, total: all.length, limit: 50 });
  }),

  http.get(`${PORTAL_BASE}/scripts/:id`, ({ params }) => {
    const id = String(params.id);
    const contract = contracts[id];
    if (!contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    // The live source and the contract that code was written against travel
    // with the document for the owner: the editor opens the one and the dry-run
    // form binds the other. The live version is the latest saved one, which is
    // the version a run executes.
    const live = (versions[id] ?? [])[0];
    return HttpResponse.json({
      contract,
      owned: true,
      source: live?.source ?? "",
      draft_params: contract.params,
    });
  }),

  // Documenting the script (#1369). It applies at once; the record and the
  // contract both move, because the page reads the contract and the listing
  // reads the record.
  http.put(`${PORTAL_BASE}/scripts/:id/metadata`, async ({ params, request }) => {
    const id = String(params.id);
    const body = (await request.json()) as {
      display_name?: string;
      description?: string;
      category?: string;
      tags?: string[];
    };
    const script = scripts.find((s) => s.id === id);
    const contract = contracts[id];
    if (!script || !contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    if (body.category && !/^[a-z][a-z0-9-]{0,30}$/.test(body.category)) {
      return HttpResponse.json(
        {
          detail:
            "category must be at most 31 characters of lowercase letters, digits, and hyphens, starting with a letter",
        },
        { status: 400 },
      );
    }
    // The server versions an edit only when it MOVED a versioned field
    // (SnapshotChanged), and the advisory is taken from the description the
    // write leaves behind rather than from the request — an edit that changes
    // only the category still carries the advisory when the stored description
    // is over the threshold. A mock that bumped on every request, or read the
    // notice off the body, would let a page keyed on either pass here and be
    // wrong against the real API.
    const moved =
      (body.display_name !== undefined && body.display_name !== script.display_name) ||
      (body.description !== undefined && body.description !== script.description) ||
      (body.category !== undefined && body.category !== script.category) ||
      (body.tags !== undefined && body.tags.join(",") !== (script.tags ?? []).join(","));
    for (const target of [script, contract]) {
      if (body.display_name !== undefined) target.display_name = body.display_name;
      if (body.description !== undefined) target.description = body.description;
      if (body.category !== undefined) target.category = body.category;
      if (body.tags !== undefined) target.tags = body.tags;
    }
    if (moved) {
      script.version += 1;
      contract.version = script.version;
    }
    return HttpResponse.json({
      version: script.version,
      description_notice: descriptionNotice(script.description),
      message: "Saved. This changes what the script says about itself and not what it does.",
    });
  }),

  // Moving a script to another person (#1404). It is an administrator's
  // action, and the version it records is what carries the authority a run
  // presents from then on.
  http.put(`${PORTAL_BASE}/scripts/:id/owner`, async ({ params, request }) => {
    const id = String(params.id);
    const body = (await request.json()) as { owner_email?: string };
    const script = scripts.find((s) => s.id === id);
    const contract = contracts[id];
    if (!script || !contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    const to = (body.owner_email ?? "").trim().toLowerCase();
    if (to === "" || !to.includes("@")) {
      return HttpResponse.json(
        { detail: "the new owner is not a usable address" },
        { status: 400 },
      );
    }
    if (to === script.owner_email.toLowerCase()) {
      return HttpResponse.json(
        { detail: `script "${script.name}" already belongs to ${to}` },
        { status: 400 },
      );
    }
    if (scripts.some((s) => s.id !== id && s.name === script.name && s.owner_email === to)) {
      return HttpResponse.json(
        { detail: `${to} already keeps a script named "${script.name}"` },
        { status: 409 },
      );
    }
    script.owner_email = to;
    contract.owner_email = to;
    script.version += 1;
    contract.version = script.version;
    return HttpResponse.json({
      owner_email: to,
      version: script.version,
      message: `${script.name} now belongs to ${to} and runs with the access you hold, captured now.`,
    });
  }),

  // Editing the code (#1307). Every save applies: the saved version is the
  // version a run executes, which is the server's rule and what the editor's
  // outcome message states.
  http.put(`${PORTAL_BASE}/scripts/:id/source`, async ({ params, request }) => {
    const id = String(params.id);
    const body = (await request.json()) as { source?: string };
    if (!body.source?.trim()) {
      return HttpResponse.json(
        { detail: "the source does not parse, so it was not saved" },
        { status: 400 },
      );
    }
    const script = scripts.find((s) => s.id === id);
    const contract = contracts[id];
    if (!script || !contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    const list = versions[id] ?? [];
    const next = Math.max(0, ...list.map((v) => v.version)) + 1;
    list.unshift({
      ...list[0]!,
      id: `${id}-v${next}`,
      version: next,
      source: body.source,
      status: "applied",
      author: "sarah.chen@example.com",
      created_at: new Date().toISOString(),
    });
    versions[id] = list;
    script.version = next;
    contract.version = next;
    return HttpResponse.json({
      applied: true,
      message:
        "Saved. This is the version that runs: run_script executes it and any schedule " +
        "fires it, presenting the roles you held at this save.",
    });
  }),

  // The owner's exercise loop (#1361, #1363, #1364): the connections a
  // parameter may name, a run of the latest saved version, and the two checks
  // an author makes before saving an edit.

  // The set a connection parameter chooses from: the connections the caller's
  // persona reaches, narrowed to the kind the parameter binds. One set for
  // every form on the page — a run and a dry run are both authorized by the
  // persona filter at query time.
  http.get(`${PORTAL_BASE}/scripts/:id/connections`, ({ params }) => {
    const id = String(params.id);
    if (!mockScriptContracts[id]) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    return HttpResponse.json({
      data: mockBindableConnections,
      source: "persona",
      note:
        "These are the connections your persona reaches that a script may query. " +
        "A run is authorized at query time, so a connection you cannot reach is not offered.",
    });
  }),

  // Running the script now. It queues what run_script queues; the response
  // carries the run id and never a result, because a worker executes it and
  // the history is where it is followed.
  http.post(`${PORTAL_BASE}/scripts/:id/runs`, async ({ params, request }) => {
    const id = String(params.id);
    const contract = contracts[id];
    if (!contract) {
      return HttpResponse.json({ detail: "script not found" }, { status: 404 });
    }
    if (contract.refusal) {
      return HttpResponse.json({ detail: contract.refusal }, { status: 400 });
    }
    await request.json();
    return HttpResponse.json(
      {
        run_id: `dpx_${Date.now().toString(36)}`,
        status: "pending",
        version: contract.version,
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
        tools: [],
        dynamic_connections: false,
        dynamic_destinations: false,
        dynamic_tools: false,
      });
    }
    return HttpResponse.json({
      ok: true,
      findings: [],
      capabilities: referencedIn(source, [
        "platform.query",
        "platform.export",
        "platform.publish_data",
        "platform.call",
      ]),
      connections: referencedIn(source, mockConnectionNames),
      destinations: source.includes("platform.export") ? ["portal"] : [],
      tools: calledTools(source),
      dynamic_connections: false,
      dynamic_destinations: false,
      dynamic_tools: false,
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
];
