// Types matching Go response structs
//
// Audit events, aggregate statistics, and the knowledge insights /
// changesets domain.

// Matches Go audit.Event. Fields with omitempty in Go are optional here
// because the backend omits them when empty/nil.
export interface AuditEvent {
  id: string;
  timestamp: string;
  duration_ms: number;
  request_id: string;
  session_id: string;
  user_id: string;
  user_email?: string;
  persona?: string;
  tool_name: string;
  toolkit_kind?: string;
  toolkit_name?: string;
  connection?: string;
  /**
   * The one sentence the caller gave for why this call was made (#1317).
   * Absent on tools the platform does not gate, on callers that cannot thread
   * arguments (MCP Apps, script runs, the REST shim), and on rows written
   * before the column existed.
   */
  purpose?: string;
  parameters?: Record<string, unknown>;
  success: boolean;
  error_message?: string;
  response_chars: number;
  request_chars: number;
  content_blocks: number;
  transport: string;
  source: string;
  enrichment_applied: boolean;
  authorized: boolean;
}

export interface AuditEventResponse {
  data: AuditEvent[];
  total: number;
  page: number;
  per_page: number;
}

export interface TimeseriesBucket {
  bucket: string;
  count: number;
  success_count: number;
  error_count: number;
  avg_duration_ms: number;
}

export interface BreakdownEntry {
  dimension: string;
  count: number;
  success_rate: number;
  avg_duration_ms: number;
}

export interface Overview {
  total_calls: number;
  success_rate: number;
  avg_duration_ms: number;
  unique_users: number;
  unique_tools: number;
  enrichment_rate: number;
  error_count: number;
}

export interface PerformanceStats {
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  avg_ms: number;
  max_ms: number;
  avg_response_chars: number;
  avg_request_chars: number;
}

export type Resolution = "minute" | "hour" | "day";
export type BreakdownDimension =
  | "tool_name"
  | "user_id"
  | "persona"
  | "toolkit_kind"
  | "connection";

export interface AuditStatsResponse {
  total: number;
  success: number;
  failures: number;
}

export interface AuditFiltersResponse {
  users: string[];
  tools: string[];
  toolkit_kinds: string[];
  sources: string[];
  user_labels?: Record<string, string>;
}

export type AuditSortColumn =
  | "timestamp"
  | "user_id"
  | "tool_name"
  | "toolkit_kind"
  | "source"
  | "connection"
  | "duration_ms"
  | "success"
  | "enrichment_applied";

export type SortOrder = "asc" | "desc";

// ---------------------------------------------------------------------------
// Knowledge — Insights & Changesets
// ---------------------------------------------------------------------------

export interface SuggestedAction {
  action_type: string;
  target: string;
  detail: string;
}

export interface RelatedColumn {
  urn: string;
  column: string;
  relevance: string;
}

/**
 * ClaimConflict is the advisory marker raised when a pending claim states a
 * number and the table currently estimates a different one. It never blocks a
 * promotion: the reviewer decides.
 */
export interface ClaimConflict {
  claimed_rows: number;
  observed_rows: number;
  message: string;
}

/**
 * ObservedEntity is the warehouse state the platform observed for one entity a
 * pending insight is about: what it is queryable as, and how many rows it holds
 * right now. Present only for a URN the query provider resolved as available.
 */
export interface ObservedEntity {
  urn: string;
  query_table?: string;
  connection?: string;
  estimated_rows?: number;
  conflict?: ClaimConflict;
}

export interface Insight {
  id: string;
  created_at: string;
  session_id: string;
  captured_by: string;
  persona: string;
  category: string;
  insight_text: string;
  confidence: string;
  entity_urns: string[];
  related_columns: RelatedColumn[];
  suggested_actions: SuggestedAction[];
  status: string;
  reviewed_by?: string;
  reviewed_at?: string;
  review_notes?: string;
  applied_by?: string;
  applied_at?: string;
  changeset_ref?: string;
  /**
   * The warehouse state observed for this insight's entities (#1219). Served
   * only while the insight is pending and only for URNs the query provider
   * resolved as available, so it is absent on most insights.
   */
  observed_entities?: ObservedEntity[];
}

export interface InsightListResponse {
  data: Insight[];
  total: number;
  page: number;
  per_page: number;
}

export interface EntityInsightSummary {
  entity_urn: string;
  count: number;
  categories: string[];
  latest_at: string;
}

export interface InsightStats {
  total_pending: number;
  by_entity: EntityInsightSummary[];
  by_category: Record<string, number>;
  by_confidence: Record<string, number>;
  by_status: Record<string, number>;
  /** created_at of the oldest pending insight; absent when the queue is empty. */
  oldest_pending_at?: string;
  /** Pending insights aged 30 or more days: the accumulating review debt. */
  pending_over_30d?: number;
}

export interface Changeset {
  id: string;
  created_at: string;
  target_urn: string;
  change_type: string;
  previous_value: Record<string, unknown>;
  new_value: Record<string, unknown>;
  source_insight_ids: string[];
  approved_by: string;
  applied_by: string;
  rolled_back: boolean;
  rolled_back_by?: string;
  rolled_back_at?: string;
}

export interface ChangesetListResponse {
  data: Changeset[];
  total: number;
  page: number;
  per_page: number;
}

export type InsightCategory =
  | "correction"
  | "business_context"
  | "data_quality"
  | "usage_guidance"
  | "relationship"
  | "enhancement";

export type InsightConfidence = "high" | "medium" | "low";

export type InsightStatus =
  | "pending"
  | "approved"
  | "rejected"
  | "applied"
  | "superseded"
  | "rolled_back";
