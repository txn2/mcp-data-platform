// Types matching Go response structs

export interface SystemInfo {
  name: string;
  version: string;
  description: string;
  transport: string;
  config_mode: string;
  portal_title: string;
  features: SystemFeatures;
  toolkit_count: number;
  persona_count: number;
}

export interface SystemFeatures {
  audit: boolean;
  oauth: boolean;
  knowledge: boolean;
  admin: boolean;
  database: boolean;
}

export interface ToolInfo {
  name: string;
  toolkit: string;
  kind: string;
  connection: string;
}

export interface ToolListResponse {
  tools: ToolInfo[];
  total: number;
}

export interface ConnectionInfo {
  kind: string;
  name: string;
  connection: string;
  tools: string[];
}

export interface ConnectionListResponse {
  connections: ConnectionInfo[];
  total: number;
}

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
}

export type AuditSortColumn =
  | "timestamp"
  | "user_id"
  | "tool_name"
  | "toolkit_kind"
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

// ---------------------------------------------------------------------------
// Tools — Schema & Execution
// ---------------------------------------------------------------------------

/** JSON Schema property for a single tool parameter. */
export interface ToolParameterSchema {
  type: "string" | "integer" | "boolean";
  description: string;
  format?: "sql" | "urn";
  enum?: string[];
  default?: string | number | boolean;
}

/** Full schema for a tool including input parameters. */
export interface ToolSchema {
  name: string;
  kind: string;
  description: string;
  parameters: {
    type: "object";
    required: string[];
    properties: Record<string, ToolParameterSchema>;
  };
}

/** Batch response from GET /tools/schemas. */
export interface ToolSchemaMap {
  schemas: Record<string, ToolSchema>;
}

/** Request body for POST /tools/call. */
export interface ToolCallRequest {
  tool_name: string;
  connection: string;
  parameters: Record<string, unknown>;
}

/** A content block in the MCP response. */
export interface ToolContentBlock {
  type: "text";
  text: string;
}

/** Response from POST /tools/call. Mirrors MCP CallToolResult. */
export interface ToolCallResponse {
  content: ToolContentBlock[];
  is_error: boolean;
  duration_ms: number;
}

// ---------------------------------------------------------------------------
// Personas
// ---------------------------------------------------------------------------

export interface PersonaPrompts {
  system_prefix?: string;
  system_suffix?: string;
  instructions?: string;
}

export interface PersonaSummary {
  name: string;
  display_name: string;
  description?: string;
  roles: string[];
  tool_count: number;
}

export interface PersonaDetail {
  name: string;
  display_name: string;
  description?: string;
  roles: string[];
  priority: number;
  allow_tools: string[];
  deny_tools: string[];
  tools: string[];
  prompts?: PersonaPrompts;
  hints?: Record<string, string>;
}

export interface PersonaListResponse {
  personas: PersonaSummary[];
  total: number;
}

export interface PersonaCreateRequest {
  name: string;
  display_name: string;
  description?: string;
  roles: string[];
  allow_tools: string[];
  deny_tools?: string[];
  priority?: number;
}

