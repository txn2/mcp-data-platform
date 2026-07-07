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

export interface PersonaTestAccessRequest {
  tool_name: string;
}

export interface PersonaTestAccessResult {
  allowed: boolean;
  matched_pattern: string;
  source: PersonaAccessSource;
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
