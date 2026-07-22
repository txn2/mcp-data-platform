// Types matching Go response structs
//
// Admin-scoped assets, API keys, platform config entries, DB-managed
// connection instances, and prompts.

// ---------------------------------------------------------------------------
// Assets (admin-scoped)
// ---------------------------------------------------------------------------

export { type Asset, type ShareSummary } from "@/api/portal/types";

export interface AdminAssetListResponse {
  data: import("@/api/portal/types").Asset[];
  total: number;
  limit: number;
  offset: number;
  share_summaries?: Record<string, import("@/api/portal/types").ShareSummary>;
}


// ---------------------------------------------------------------------------
// API Keys
// ---------------------------------------------------------------------------

export interface APIKeySummary {
  name: string;
  email?: string;
  description?: string;
  roles: string[];
  expires_at?: string;
  expired?: boolean;
  source?: "file" | "database" | "both";
}

export interface APIKeyListResponse {
  keys: APIKeySummary[];
  total: number;
}

export interface APIKeyCreateResponse {
  name: string;
  email?: string;
  description?: string;
  key: string;
  roles: string[];
  expires_at?: string;
  warning: string;
}

// --- Config entries ---

export interface ConfigEntry {
  key: string;
  value: string;
  updated_by: string;
  updated_at: string;
}

export interface ConfigChangelogEntry {
  id: number;
  key: string;
  action: string;
  value?: string;
  changed_by: string;
  changed_at: string;
}

// ConfigChangelogListResponse is the paginated changelog envelope: a page of
// entries plus the full-history total so the UI can page through every change.
export interface ConfigChangelogListResponse {
  entries: ConfigChangelogEntry[];
  total: number;
}

export interface EffectiveConfigEntry {
  key: string;
  value: string;
  source: "file" | "database";
  updated_by?: string;
  updated_at?: string;
}

// AgentInstructionsBaseline is the platform-owned "how to operate" instruction
// baseline (#646) composed beneath the admin's agent_instructions. It names only
// tools this deployment exposes, so admins see what is already covered.
export interface AgentInstructionsBaseline {
  baseline: string;
}

// ---------------------------------------------------------------------------
// Connection Instances (DB-managed)
// ---------------------------------------------------------------------------

export interface ConnectionInstance {
  kind: string;
  name: string;
  config: Record<string, unknown>;
  description: string;
  created_by: string;
  updated_at: string;
}

// ---------------------------------------------------------------------------
// Prompts
// ---------------------------------------------------------------------------

export interface PromptArgument {
  name: string;
  description: string;
  required: boolean;
}

export interface Prompt {
  id: string;
  name: string;
  display_name: string;
  description: string;
  content: string;
  arguments: PromptArgument[];
  category: string;
  scope: "global" | "persona" | "personal" | "system";
  personas: string[];
  tags: string[];
  status: "draft" | "approved" | "deprecated" | "superseded";
  approved_by?: string;
  approved_at?: string;
  deprecated_at?: string;
  superseded_by?: string;
  review_requested?: boolean;
  requested_scope?: string;
  requested_personas?: string[];
  owner_email: string;
  source: string;
  enabled: boolean;
  /** Collection the prompt belongs to (at most one); absent = uncollected. */
  collection_id?: string;
  /** Number of the snapshot the live row currently serves (#1009). */
  version?: number;
  created_at: string;
  updated_at: string;
}

/** One immutable snapshot of a prompt's versioned fields, with the author and
 *  the approval stamp bound to that specific version (#1009). */
export interface PromptVersion {
  id: string;
  prompt_id: string;
  version: number;
  display_name: string;
  description: string;
  content: string;
  arguments: PromptArgument[];
  tags: string[];
  author: string;
  status: "draft" | "applied" | "superseded" | "rejected";
  approved_by?: string;
  approved_at?: string;
  created_at: string;
}

/** Named group organizing the prompt library by team, domain, or workflow. */
export interface PromptCollection {
  id: string;
  name: string;
  description: string;
  created_by: string;
  prompt_count: number;
  created_at: string;
  updated_at: string;
}

/** Audit-derived usage rollup for one prompt (#1009). */
export interface PromptUsage {
  run_count: number;
  last_run_at?: string;
}

export interface PromptListResponse {
  data: Prompt[];
  total: number;
}
