// Types matching Go response structs
//
// Tool schemas & execution, personas, tool master-detail, and the
// known-users directory.

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
  title?: string;
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

export interface PersonaContextOverrides {
  description_prefix?: string;
  description_override?: string;
  agent_instructions_suffix?: string;
  agent_instructions_override?: string;
}

export interface PersonaSummary {
  name: string;
  display_name: string;
  description?: string;
  roles: string[];
  tool_count: number;
  source?: "file" | "database" | "both";
}

/**
 * One per-(connection, method, path) rule for the HTTP API gateway. Layered on
 * top of the connection grant: a connection no rule names keeps the
 * connection-level decision as its only gate.
 */
export interface APIRouteRule {
  /** Glob matched against the connection name. Required. */
  connection: string;
  /** HTTP method globs, uppercase. Empty or absent matches any method. */
  methods?: string[];
  /**
   * Path globs. Each is matched against both the path a call reaches
   * ("/v1/orders/42") and the catalog path the operation declares
   * ("/v1/orders/{id}"), so naming the declared path governs that one
   * operation. Empty or absent matches any path.
   */
  paths?: string[];
  /** "allow" (the default) or "deny". Deny wins. */
  action?: "allow" | "deny";
}

export interface PersonaDetail {
  name: string;
  display_name: string;
  description?: string;
  roles: string[];
  priority: number;
  allow_tools: string[];
  deny_tools: string[];
  allow_connections?: string[];
  deny_connections?: string[];
  tools: string[];
  context?: PersonaContextOverrides;
  source?: "file" | "database" | "both";
  /** Always an array, never null. */
  api_routes: APIRouteRule[];
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
  allow_connections?: string[];
  deny_connections?: string[];
  priority?: number;
  description_prefix?: string;
  description_override?: string;
  agent_instructions_suffix?: string;
  agent_instructions_override?: string;
  /** Replaces the persona's route rules wholesale. Absent leaves it with none. */
  api_routes?: APIRouteRule[];
}

/** One api-kind connection and every operation its catalog declares. */
export interface APIRouteConnection {
  name: string;
  description?: string;
  base_url?: string;
  auth_mode?: string;
  catalog_id?: string;
  /**
   * The connection's whole index, narrowed by no persona. Empty for a
   * connection with no catalog, whose rules can be written as patterns but not
   * selected.
   */
  operations: APIRouteOperation[];
}

/** One operation a rule can be written against. */
export interface APIRouteOperation {
  operation_id: string;
  method: string;
  /** The path as the catalog declares it, placeholders included. */
  path: string;
  summary?: string;
  tags?: string[];
  spec?: string;
}

export interface APIRouteConnectionList {
  connections: APIRouteConnection[];
  total: number;
}

// --- Tools master-detail (issue #340) ---

export type PersonaAccessSource = "allow" | "deny" | "default";

export interface ToolPersonaAccess {
  persona: string;
  /** End-to-end: tool rule allows AND connection rule allows. */
  allowed: boolean;
  matched_pattern?: string;
  /** Source of the tool-rule decision (not the connection check). */
  source: PersonaAccessSource;
  /** Independent: whether this persona allows the tool's connection. */
  connection_allowed: boolean;
}

export interface ToolActivityAggregate {
  window_seconds: number;
  call_count: number;
  success_rate: number;
  avg_duration_ms: number;
}

export interface ToolDetail {
  name: string;
  title?: string;
  description: string;
  toolkit_kind: string;
  toolkit_name?: string;
  connection?: string;
  input_schema?: unknown;
  personas: ToolPersonaAccess[];
  hidden_by_global_deny: boolean;
  global_deny_pattern?: string;
  hidden_by_persona?: Record<string, boolean>;
  description_overridden: boolean;
  override_author?: string;
  activity?: ToolActivityAggregate;
  enrichment_rule_count: number;
}

/**
 * Two questions share the test-access route: `tool_name` asks whether the
 * persona may call a tool, `connection` + `method` + `path` whether it may
 * invoke that operation.
 */
export interface PersonaTestAccessRequest {
  tool_name?: string;
  connection?: string;
  method?: string;
  path?: string;
}

export interface PersonaTestAccessResult {
  allowed: boolean;
  matched_pattern?: string;
  source: PersonaAccessSource;
  /** The rule that decided an API route question. Absent for the tool case. */
  matched_rule?: APIRouteRule;
}

export interface ToolVisibilityRequest {
  hidden: boolean;
}

export interface ToolVisibilityResponse {
  tool_name: string;
  hidden: boolean;
  deny: string[];
}

// --- Known-users directory (#614) ---

export interface DirectoryUser {
  email: string;
  first_name: string;
  last_name: string;
  source: string; // "auth" | "admin"
  confirmed: boolean;
  added_by?: string;
  last_seen_at?: string;
  created_at: string;
  updated_at: string;
}

export interface UserListResponse {
  users: DirectoryUser[];
  total: number;
}

export interface UserCreateRequest {
  email: string;
  first_name?: string;
  last_name?: string;
}

export interface UserUpdateRequest {
  first_name?: string;
  last_name?: string;
}
