// Session read model (#1318). A session is not a stored row: it is every
// audit event sharing one session id, so these shapes mirror
// internal/platform/sessionview rather than a table.

/** Origin of a session id, derived from its prefix. */
export type SessionKind = "agent" | "portal" | "script" | "transport";

export interface SessionSummary {
  session_id: string;
  kind: SessionKind;
  user_id: string;
  user_email?: string;
  persona?: string;
  started_at: string;
  last_active_at: string;
  call_count: number;
  failure_count: number;
  tools: string[];
  connections: string[];
  asset_count: number;
  insight_count: number;
}

/** One call the session made, in the order it was made. */
export interface SessionTimelineEntry {
  event_id: string;
  timestamp: string;
  tool_name: string;
  purpose?: string;
  toolkit_kind?: string;
  connection?: string;
  success: boolean;
  error_message?: string;
  duration_ms: number;
}

export interface SessionAssetRef {
  id: string;
  name: string;
  content_type: string;
  created_at: string;
}

export interface SessionInsightRef {
  id: string;
  category: string;
  text: string;
  status: string;
  created_at: string;
}

export interface SessionDetail extends SessionSummary {
  assets: SessionAssetRef[];
  insights: SessionInsightRef[];
  timeline: SessionTimelineEntry[];
  timeline_total: number;
}

export interface SessionListResponse {
  data: SessionSummary[];
  total: number;
  page: number;
  per_page: number;
}
