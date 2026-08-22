import type { AuditEvent, CallKind, CallRecord } from "@/api/admin/types";
import type { ProvenanceCall } from "@/api/portal/types";
import { MOCK_CALLER_EMAIL, mockAuditEvents } from "./audit";

// The mock call catalog is derived from the mock audit events exactly as the
// server derives it from audit_logs, so a record opened in the UI carries the
// session, the caller and the statement the events tab shows for the same call.
//
// The outcomes are derived here too, by the same rule the SQL uses: a record an
// asset NAMED is satisfied, a read a later read of the same resource replaced is
// superseded, a failed call is failed, and everything else merely ran. What the
// fixture chooses is which calls the mock assets name; the outcomes follow from
// that, so the mock cannot show a state the server could not produce.
//
// Two parts of the rule are easy to lose and are spelled out here because the
// server spells them out too (#1352, #1353): a target carries the resolved path
// parameters, so one endpoint addressing two resources is two targets; and only
// a read can be superseded, because a mutation is not a better version of an
// earlier mutation.

/** The tools the catalog records, mirroring internal/platform/callrecord. */
const RECORDED_TOOLS: Record<string, CallKind> = {
  trino_query: "sql",
  trino_execute: "sql",
  trino_export: "sql",
  api_invoke_endpoint: "api",
  api_export: "api",
};

/**
 * How many of the caller's own records each citing artifact names. An export
 * names one call, the statement it streamed into the file; a save names the set
 * the agent passed as `sources`.
 */
const CITED_PER_ASSET: Record<"asset" | "export", number> = { asset: 2, export: 1 };

/**
 * The assets whose provenance cites recorded calls. Asset ids and names are
 * the fixture's own; mocks/conformance.test.ts holds them to mockAssets so the
 * two fixtures cannot drift apart.
 */
const CITING_ASSETS = [
  { assetId: "ast-001", assetName: "Q4 Revenue Dashboard", kind: "asset" as const },
  { assetId: "ast-003", assetName: "Weekly Inventory Report", kind: "export" as const },
];

/** A capture that named a call: the agent's own verdict on its query. */
const CAPTURE_ARTIFACT = {
  kind: "capture" as const,
  id: "ins-001",
  name: "Revenue by region excludes canceled orders; filter on status.",
};

/**
 * One {name} slot of an operation's path template. Two spellings because a
 * global regex carries `lastIndex` state across `.test()` calls: substitution
 * needs the global flag, and asking whether a slot survived must not depend on
 * where the previous question left off.
 */
const PLACEHOLDER_ALL = /\{[^{}/]+\}/g;
const PLACEHOLDER = /\{[^{}/]+\}/;

/**
 * The resource an API call addressed: the operation id with its path parameters
 * substituted in, and the parameters no slot consumed appended. A template slot
 * nothing resolved leaves no target at all, which is how the server says "this
 * call cannot be told apart from a different one".
 */
function apiTarget(event: AuditEvent): string[] {
  const operation = String(event.parameters?.["operation_id"] ?? "");
  if (!operation) return [];
  const params = (event.parameters?.["path_params"] ?? {}) as Record<string, string>;
  const used = new Set<string>();
  const resolved = operation.replace(PLACEHOLDER_ALL, (slot) => {
    const name = slot.slice(1, -1);
    const value = (params[name] ?? "").trim();
    if (!value) return slot;
    used.add(name);
    return value;
  });
  if (PLACEHOLDER.test(resolved)) return [];
  // Rendered as JSON with sorted keys, matching the server: a target is an
  // identity, so a value holding a separator must not be able to make two
  // different parameter sets render alike.
  const extra = Object.entries(params)
    .filter(([name, value]) => !used.has(name) && String(value).trim() !== "")
    .sort(([a], [b]) => a.localeCompare(b));
  const suffix =
    extra.length > 0 ? `(${JSON.stringify(Object.fromEntries(extra))})` : "";
  return [`api:${event.connection}:${resolved}${suffix}`];
}

/** Extracts the dataset a mock SQL statement reads, in URN form. */
function targetsOf(event: AuditEvent, kind: CallKind): string[] {
  if (kind === "api") return apiTarget(event);
  const sql = String(event.parameters?.["sql"] ?? "");
  const match = sql.match(/FROM\s+([a-zA-Z_][\w.]*)/i);
  if (!match) return [];
  const name = match[1]!.split(".").length === 3 ? match[1]! : `iceberg.${match[1]!}`;
  return [`urn:li:dataset:(urn:li:dataPlatform:trino,${name},PROD)`];
}

/** A statement that only reads, matching the server's own pattern. */
const READ_STATEMENT = /^[\s(]*(with|select|show|describe|desc|explain|table|values)\b/i;

/**
 * Whether a record can be superseded at all. Supersession says a later call
 * answered the same question better, which only a read does.
 */
function readShaped(record: CallRecord): boolean {
  if (record.kind === "api") return record.method === "GET" || record.method === "HEAD";
  return READ_STATEMENT.test(record.statement ?? "");
}

/** One audit event as the record the catalog would hold for it. */
function toRecord(event: AuditEvent, kind: CallKind): CallRecord {
  const sql = String(event.parameters?.["sql"] ?? "");
  return {
    id: `call-${event.id.replace("evt-", "")}`,
    event_id: event.id,
    reference: `mcp:call:${event.id}`,
    kind,
    tool_name: event.tool_name,
    connection: event.connection,
    statement: kind === "sql" ? sql : "",
    method: kind === "api" ? String(event.parameters?.["method"] ?? "").toUpperCase() : "",
    path: kind === "api" ? String(event.parameters?.["path"] ?? "") : "",
    operation_id: kind === "api" ? String(event.parameters?.["operation_id"] ?? "") : "",
    targets: targetsOf(event, kind),
    purpose: event.purpose,
    user_id: event.user_id,
    user_email: event.user_email,
    session_id: event.session_id,
    persona: event.persona,
    success: event.success,
    error_message: event.error_message,
    duration_ms: event.duration_ms,
    response_chars: event.response_chars,
    // Filled in by deriveOutcomes, which applies the server's rule to the
    // whole set rather than to one record at a time.
    outcome: "ran",
    reuse_count: 0,
    created_at: event.timestamp,
  };
}

/**
 * Marks the records the fixture's assets and captures cite.
 *
 * Only the mock caller's own records are cited, so their own catalog is the
 * one that shows every outcome and a non-empty review queue: a user surface
 * demonstrating nothing but "ran" would demonstrate nothing.
 */
function applyCitations(records: CallRecord[]): void {
  const eligible = records.filter(
    (r) => r.success && r.statement && r.user_id === MOCK_CALLER_EMAIL,
  );
  let next = 0;
  for (const asset of CITING_ASSETS) {
    for (let n = 0; n < CITED_PER_ASSET[asset.kind]; n++) {
      const record = eligible[next++];
      if (!record) return;
      record.satisfied_by = asset.kind;
      record.artifacts = [{ kind: asset.kind, id: asset.assetId, name: asset.assetName }];
      record.reuse_count = next === 1 ? 3 : 0;
    }
  }
  // One more by the capture route: an answer that went into the conversation
  // and was recorded by the agent rather than saved.
  const captured = eligible[next];
  if (!captured) return;
  captured.satisfied_by = "capture";
  captured.artifacts = [CAPTURE_ARTIFACT];
  captured.reuse_count = 2;
}

/**
 * Names the calls each asset cites, so the asset fixture can record the same
 * ids in its own provenance. Without this the catalog would show a record as
 * satisfied by an asset whose provenance did not mention it, which is a state
 * the server cannot produce.
 */
export function citedEventIDs(assetId: string): string[] {
  return mockCallRecords
    .filter((r) => r.artifacts?.some((a) => a.id === assetId))
    .map((r) => r.event_id);
}

/**
 * The captured calls an asset's citation names, built from the very records it
 * cites. Every producer of a capture drops one whose call list is empty
 * (pkg/toolkits/portal, internal/platform/exportadapters), so a capture that
 * named event ids and carried no calls modelled a state the server cannot
 * write — and the asset viewer now leads with the newest capture, where an
 * empty one would render as a heading over nothing (#1422).
 */
export function citedProvenanceCalls(assetId: string): ProvenanceCall[] {
  return mockCallRecords
    .filter((r) => r.artifacts?.some((a) => a.id === assetId))
    .map((r) => ({
      event_id: r.event_id,
      kind: r.kind,
      tool: r.tool_name,
      connection: r.connection,
      statement: r.statement,
      method: r.method,
      path: r.path,
      operation_id: r.operation_id,
      purpose: r.purpose,
      outcome: r.success ? ("success" as const) : ("error" as const),
      error: r.error_message,
      duration_ms: r.duration_ms,
      timestamp: r.created_at,
    }));
}

/**
 * How an asset names the calls it cites, which the asset fixture writes into
 * that asset's provenance. A save names them as a set the agent passed; an
 * export names one call, its own, inside a capture that also holds the window
 * around it. The server reads exactly this distinction (#1353).
 */
export function citationsFor(assetId: string): {
  eventIDs: string[];
  kind: "asset" | "export";
} | null {
  const asset = CITING_ASSETS.find((a) => a.assetId === assetId);
  if (!asset) return null;
  const eventIDs = citedEventIDs(assetId);
  return eventIDs.length > 0 ? { eventIDs, kind: asset.kind } : null;
}

/** Applies the derived outcome to every record, in the server's order. */
function deriveOutcomes(records: CallRecord[]): void {
  for (const record of records) {
    if (!record.success) {
      record.outcome = "failed";
      continue;
    }
    if (record.satisfied_by) {
      record.outcome = "satisfied";
      continue;
    }
    const superseded =
      readShaped(record) &&
      records.some(
        (other) =>
          other !== record &&
          other.success &&
          readShaped(other) &&
          other.session_id === record.session_id &&
          other.kind === record.kind &&
          other.connection === record.connection &&
          record.targets.length > 0 &&
          other.targets.join() === record.targets.join() &&
          Date.parse(other.created_at) > Date.parse(record.created_at),
      );
    record.outcome = superseded ? "superseded" : "ran";
  }
}

function buildRecords(): CallRecord[] {
  const records = mockAuditEvents
    .filter((event) => RECORDED_TOOLS[event.tool_name])
    .map((event) => toRecord(event, RECORDED_TOOLS[event.tool_name]!));
  applyCitations(records);
  deriveOutcomes(records);
  return records.sort(
    (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
  );
}

export const mockCallRecords: CallRecord[] = buildRecords();

/** The assets the fixture's records cite, for the conformance check. */
export const mockCitingAssets = CITING_ASSETS;
