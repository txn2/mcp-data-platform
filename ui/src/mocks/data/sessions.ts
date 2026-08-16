import type {
  AuditEvent,
  SessionDetail,
  SessionKind,
  SessionSummary,
  SessionTimelineEntry,
} from "@/api/admin/types";
import { mockAuditEvents } from "./audit";
import { mockAssets } from "./assets";
import { mockInsights } from "./knowledge";

// The mock sessions are rolled up from the mock audit events exactly as the
// server rolls them up from audit_logs, so the events tab, the drawer's session
// link, and the sessions list agree in dev and in the screenshots.

function kindOf(id: string): SessionKind {
  if (id.startsWith("dps_")) return "agent";
  if (id.startsWith("dpp_")) return "portal";
  if (id.startsWith("dpx_")) return "script";
  return "transport";
}

/** The assets a mock session saved, matched on the asset's own session id. */
function assetsOf(sessionId: string) {
  return mockAssets
    .filter((a) => a.session_id === sessionId)
    .map((a) => ({
      id: a.id,
      name: a.name,
      content_type: a.content_type,
      created_at: a.created_at,
    }));
}

/** The insights a mock session captured. */
function insightsOf(sessionId: string) {
  return mockInsights
    .filter((i) => i.session_id === sessionId)
    .map((i) => ({
      id: i.id,
      category: i.category,
      text: i.insight_text,
      status: i.status,
      created_at: i.created_at,
    }));
}

function summarize(sessionId: string, events: AuditEvent[]): SessionSummary {
  const ordered = [...events].sort(
    (a, b) => Date.parse(a.timestamp) - Date.parse(b.timestamp),
  );
  const first = ordered[0]!;
  const last = ordered[ordered.length - 1]!;
  return {
    session_id: sessionId,
    kind: kindOf(sessionId),
    user_id: first.user_id,
    user_email: first.user_email,
    persona: first.persona,
    started_at: first.timestamp,
    last_active_at: last.timestamp,
    call_count: ordered.length,
    failure_count: ordered.filter((e) => !e.success).length,
    tools: [...new Set(ordered.map((e) => e.tool_name))].sort(),
    connections: [
      ...new Set(ordered.map((e) => e.connection).filter(Boolean)),
    ].sort() as string[],
    asset_count: assetsOf(sessionId).length,
    insight_count: insightsOf(sessionId).length,
  };
}

function group(): Map<string, AuditEvent[]> {
  const byID = new Map<string, AuditEvent[]>();
  for (const event of mockAuditEvents) {
    if (!event.session_id) continue;
    const bucket = byID.get(event.session_id);
    if (bucket) bucket.push(event);
    else byID.set(event.session_id, [event]);
  }
  return byID;
}

const grouped = group();

/** Every mock session, most recently active first. */
export const mockSessions: SessionSummary[] = [...grouped.entries()]
  .map(([id, events]) => summarize(id, events))
  .sort((a, b) => Date.parse(b.last_active_at) - Date.parse(a.last_active_at));

function timelineEntry(event: AuditEvent): SessionTimelineEntry {
  return {
    event_id: event.id,
    timestamp: event.timestamp,
    tool_name: event.tool_name,
    purpose: event.purpose,
    toolkit_kind: event.toolkit_kind,
    connection: event.connection,
    success: event.success,
    error_message: event.error_message,
    duration_ms: event.duration_ms,
  };
}

/** One mock session in full, or undefined when nothing was recorded for it. */
export function mockSessionDetail(
  sessionId: string,
  page: number,
  perPage: number,
): SessionDetail | undefined {
  const events = grouped.get(sessionId);
  if (!events) return undefined;

  const ordered = [...events].sort(
    (a, b) => Date.parse(a.timestamp) - Date.parse(b.timestamp),
  );
  const start = (page - 1) * perPage;
  return {
    ...summarize(sessionId, ordered),
    assets: assetsOf(sessionId),
    insights: insightsOf(sessionId),
    timeline: ordered.slice(start, start + perPage).map(timelineEntry),
    timeline_total: ordered.length,
  };
}
