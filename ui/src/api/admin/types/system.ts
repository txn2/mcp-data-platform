// Types matching Go response structs
//
// System info, tool/connection listing, connection health, and the
// gateway / OAuth / enrichment domain.

export interface SystemInfo {
  name: string;
  version: string;
  commit: string;
  build_date: string;
  description: string;
  transport: string;
  config_mode: string;
  portal_title: string;
  brand_name?: string;
  brand_url?: string;
  version_url?: string;
  portal_logo: string;
  portal_logo_light: string;
  portal_logo_dark: string;
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
  title?: string;
  toolkit: string;
  kind: string;
  connection: string;
  /** True when the tool is excluded from tools/list by tools.allow / tools.deny. */
  hidden: boolean;
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
  hidden_tools: string[];
  // Present for gateway (mcp) connections; mirrors the Go connectionInfo
  // and swagger admin.connectionInfo shape. See ConnectionHealth.
  health?: ConnectionHealth;
}

export interface ConnectionListResponse {
  connections: ConnectionInfo[];
  total: number;
}

// ConnectionHealth is a gateway upstream's runtime reachability, mirroring the
// list_connections MCP tool's health shape so the admin UI and the tool report
// the same state for the same connection. Present only for connection kinds
// that hold a live session (gateway); omitted otherwise.
export interface ConnectionHealth {
  reachable: boolean;
  last_success?: string;
  last_error?: string;
}

export interface EffectiveConnection {
  kind: string;
  name: string;
  connection: string;
  description?: string;
  source: "file" | "database" | "both";
  tools: string[];
  config?: Record<string, unknown>;
  created_by?: string;
  updated_at?: string;
  health?: ConnectionHealth;
  // file_declared marks a connection the platform configuration file declares.
  // source cannot carry it: the connection backfill seeds a stored row for
  // every file-configured connection, so those report "both" as well. The file
  // owns such a connection, and deleting it is refused.
  file_declared?: boolean;
}

// ---------------------------------------------------------------------------
// Gateway (third-party MCP proxy)
// ---------------------------------------------------------------------------

export interface GatewayProbeTool {
  name: string;
  local_name: string;
  description?: string;
}

export interface GatewayTestResponse {
  healthy: boolean;
  tools?: GatewayProbeTool[];
  error?: string;
}

export interface GatewayRefreshResponse {
  healthy: boolean;
  tools?: string[];
  error?: string;
}

export interface GatewayOAuthStatus {
  configured: boolean;
  token_acquired: boolean;
  expires_at?: string;
  last_refreshed_at?: string;
  has_refresh_token: boolean;
  refresh_expires_at?: string;
  last_error?: string;
  grant?: string;
  token_url?: string;
  scope?: string;
  authenticated_by?: string;
  authenticated_at?: string;
  needs_reauth?: boolean;
  refresh_token_revoked?: boolean;
}

export interface GatewayOAuthStartResponse {
  authorization_url: string;
  state: string;
  redirect_uri: string;
  expires_at: string;
}

// ConnectionOAuthStatus mirrors connoauth.OAuthStatus from the backend.
// Returned by the unified /connections/{kind}/{name}/oauth-status
// endpoint for any connection kind. Distinct from GatewayOAuthStatus
// only because the backend type lives in a different package; the
// fields are intentionally a superset for portal compatibility.
// ConnectionOAuthHealthSummary is one row of the bulk
// /connections/oauth-health endpoint. Powers the connection-list
// health badge.
//
// has_oauth=false means the connection is not OAuth-configured at
// all (bearer / api_key / none); the UI hides the badge for those.
//
// idp_error_code is the RFC 6749 `error` field from the latest
// refresh_failed_* event, empty when the last refresh succeeded or
// no events exist. Drives the tooltip text so the operator sees
// "invalid_client" or "invalid_grant" without navigating away from
// the connection list.
export interface ConnectionOAuthHealthSummary {
  kind: string;
  name: string;
  has_oauth: boolean;
  needs_reauth: boolean;
  token_acquired: boolean;
  idp_error_code?: string;
}

export interface ConnectionsOAuthHealthResponse {
  connections: ConnectionOAuthHealthSummary[];
}

export interface ConnectionOAuthStatus {
  configured: boolean;
  token_acquired: boolean;
  expires_at?: string;
  last_refreshed_at?: string;
  has_refresh_token: boolean;
  refresh_expires_at?: string;
  last_error?: string;
  token_url?: string;
  scope?: string;
  authenticated_by?: string;
  authenticated_at?: string;
  needs_reauth?: boolean;
  last_revocation?: {
    occurred_at: string;
    reason?: string;
    idp_host?: string;
  };
}

// ConnectionAuthEvent mirrors authevents.Event. The History panel
// renders the most recent N of these for the active connection.
export interface ConnectionAuthEvent {
  id: string;
  occurred_at: string;
  connection_kind?: string;
  connection_name?: string;
  event_type:
    | "connect_started"
    | "connect_completed"
    | "refresh_succeeded"
    | "refresh_failed_transient"
    | "refresh_failed_revoked"
    | "refresh_skipped_no_token"
    | "refresh_skipped_expired"
    | "refresh_rotation_persistence_failed"
    | "token_deleted_revoked"
    | "token_deleted_admin";
  actor: string;
  idp_host?: string;
  detail?: Record<string, unknown>;
}

export interface GatewayConnectionStatus {
  name: string;
  healthy: boolean;
  auth_mode: string;
  tools?: string[];
  oauth?: GatewayOAuthStatus;
}

export interface EnrichmentPredicate {
  kind?: "" | "always" | "response_contains";
  paths?: string[];
}

export interface EnrichmentAction {
  source: string;
  operation: string;
  parameters?: Record<string, unknown>;
}

export interface EnrichmentMerge {
  kind?: "" | "path";
  path?: string;
}

export interface EnrichmentRule {
  id: string;
  connection_name: string;
  tool_name: string;
  when_predicate: EnrichmentPredicate;
  enrich_action: EnrichmentAction;
  merge_strategy: EnrichmentMerge;
  description?: string;
  enabled: boolean;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface EnrichmentRuleBody {
  tool_name: string;
  when_predicate: EnrichmentPredicate;
  enrich_action: EnrichmentAction;
  merge_strategy: EnrichmentMerge;
  description?: string;
  enabled: boolean;
}

export interface FiredRule {
  rule_id: string;
  source: string;
  op: string;
  skipped?: boolean;
  error?: string;
  duration_ms: number;
}

export interface DryRunRequest {
  args?: Record<string, unknown>;
  response?: unknown;
  user?: { id?: string; email?: string };
}

export interface DryRunResponse {
  response: unknown;
  warnings?: string[];
  fired?: FiredRule[];
}
