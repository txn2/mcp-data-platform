// Types matching Go response structs

export interface SystemInfo {
  name: string;
  version: string;
  description: string;
  transport: string;
  config_mode: string;
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

export interface AuditEvent {
  id: string;
  timestamp: string;
  duration_ms: number;
  request_id: string;
  session_id: string;
  user_id: string;
  user_email: string;
  persona: string;
  tool_name: string;
  toolkit_kind: string;
  toolkit_name: string;
  connection: string;
  parameters: Record<string, unknown>;
  success: boolean;
  error_message: string;
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
