import type { PortalAccess } from "@/lib/portalAccess";

/**
 * What a persona, saved or not, lets its people reach (#2257): the server's
 * evaluation of every decision a request meets, from
 * POST /api/v1/admin/personas/effective-access.
 */
export type AccountType = "internal" | "external" | "automated";
export type ToolDecision = "allow" | "deny" | "none" | "external";
export type OperationDecision =
  | "open"
  | "rule_allow"
  | "rule_deny"
  | "rule_nomatch"
  | "connection"
  | "tool"
  | "external";

export interface RoleOverlap {
  role: string;
  persona: string;
  priority: number;
}

export interface PersonaAssignment {
  roles: string[];
  /** Roles personas.role_mapping assigns to this persona outright. */
  role_mapping: string[];
  /** Roles that read the persona's folder and prompts. */
  folder_reader_roles: string[];
  /** The role that writes the persona's folder; administrators do too. */
  folder_writer_role: string;
  role_overlaps: RoleOverlap[];
}

export interface ConnectionAccess {
  name: string;
  allowed: boolean;
}

export interface ToolAccessItem {
  name: string;
  kind?: string;
  decision: ToolDecision;
  matched?: string;
  exact: boolean;
  connections: ConnectionAccess[];
  callable: boolean;
}

export interface ScratchTarget {
  catalog: string;
  schema: string;
}

export interface ConnectionAccessItem {
  name: string;
  kind: string;
  decision: Exclude<ToolDecision, "external">;
  matched?: string;
  exact: boolean;
  scratch?: ScratchTarget;
  used_by_channels: string[];
}

export interface OperationAccessItem {
  connection: string;
  method: string;
  path: string;
  decision: OperationDecision;
  rule_index: number;
  callable: boolean;
}

export interface ScratchAccess {
  connection: string;
  catalog: string;
  schema: string;
  list: boolean;
  register: boolean;
  drop_via_sql: boolean;
  registrations: number;
  queryable_from: string[];
}

export interface PersonaReferences {
  secrets: { named: string[]; unrestricted: number };
  script_grants: number;
  script_versions: number;
  prompts: number;
  shares: number;
  webhook_sources: string[];
  external_accounts: number;
  api_keys: number;
}

export interface EffectiveAccess {
  is_admin: boolean;
  /** The configured administrator persona's name, which is never external. */
  admin_persona: string;
  account_type: AccountType;
  calls_excluded_by_config: boolean;
  assignment: PersonaAssignment;
  tools: { total: number; callable: number; items: ToolAccessItem[] };
  connections: { total: number; allowed: number; items: ConnectionAccessItem[] };
  api_operations: { total: number; callable: number; items: OperationAccessItem[] };
  scratch: ScratchAccess[];
  portal: { stored: PortalAccess; effective: PortalAccess; capped_by_external: string[] };
  sharing: boolean;
  resource_scopes: string[];
  references: PersonaReferences;
  unscoped: { signed_in_endpoints: number };
}

/** Whether anything other than a secret open to every persona names it. */
export function hasReferences(r: PersonaReferences): boolean {
  return (
    r.secrets.named.length > 0 ||
    r.script_grants > 0 ||
    r.script_versions > 0 ||
    r.prompts > 0 ||
    r.shares > 0 ||
    r.webhook_sources.length > 0 ||
    r.external_accounts > 0 ||
    r.api_keys > 0
  );
}
