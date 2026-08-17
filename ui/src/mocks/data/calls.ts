import type { AuditEvent, CallKind, CallRecord } from "@/api/admin/types";
import { MOCK_CALLER_EMAIL, mockAuditEvents } from "./audit";

// The mock call catalog is derived from the mock audit events exactly as the
// server derives it from audit_logs, so a record opened in the UI carries the
// session, the caller and the statement the events tab shows for the same call.
//
// The outcomes are derived here too, by the same rule the SQL uses: a record an
// asset cites is satisfied, a record a later call in the same session replaced
// over the same targets is superseded, a failed call is failed, and everything
// else merely ran. What the fixture chooses is which calls the mock assets
// cite; the outcomes follow from that, so the mock cannot show a state the
// server could not produce.

/** The tools the catalog records, mirroring internal/platform/callrecord. */
const RECORDED_TOOLS: Record<string, CallKind> = {
  trino_query: "sql",
  trino_execute: "sql",
  trino_export: "sql",
  api_invoke_endpoint: "api",
  api_export: "api",
};

/** How many of the caller's own records the mock assets cite. */
const CITED_PER_ASSET = 2;

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

/** Extracts the dataset a mock SQL statement reads, in URN form. */
function targetsOf(event: AuditEvent, kind: CallKind): string[] {
  if (kind === "api") {
    const operation = String(event.parameters?.["operation_id"] ?? "");
    return operation ? [`api:${event.connection}:${operation}`] : [];
  }
  const sql = String(event.parameters?.["sql"] ?? "");
  const match = sql.match(/FROM\s+([a-zA-Z_][\w.]*)/i);
  if (!match) return [];
  const name = match[1]!.split(".").length === 3 ? match[1]! : `iceberg.${match[1]!}`;
  return [`urn:li:dataset:(urn:li:dataPlatform:trino,${name},PROD)`];
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
    method: kind === "api" ? String(event.parameters?.["method"] ?? "GET") : "",
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
  const satisfied = records
    .filter((r) => r.success && r.statement && r.user_id === MOCK_CALLER_EMAIL)
    .slice(0, CITED_PER_ASSET * CITING_ASSETS.length + 1);
  satisfied.forEach((record, i) => {
    if (i === satisfied.length - 1) {
      // The last one is the capture route: an answer that went into the
      // conversation and was recorded by the agent rather than saved.
      record.satisfied_by = "capture";
      record.artifacts = [CAPTURE_ARTIFACT];
      record.reuse_count = 2;
      return;
    }
    const asset = CITING_ASSETS[Math.floor(i / CITED_PER_ASSET)]!;
    record.satisfied_by = asset.kind;
    record.artifacts = [{ kind: asset.kind, id: asset.assetId, name: asset.assetName }];
    record.reuse_count = i === 0 ? 3 : 0;
  });
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
    const superseded = records.some(
      (other) =>
        other !== record &&
        other.success &&
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
