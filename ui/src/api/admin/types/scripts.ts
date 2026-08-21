// Managed-script admin types. They mirror the admin REST payloads served by
// internal/httpserver/scripthttp.

// Script is a managed script's live row. The live version is the version a run
// executes: saving a version makes it the version that runs.
export interface Script {
  id: string;
  name: string;
  display_name: string;
  description: string;
  // owner_email is the one person the script belongs to: the only caller who
  // sees it, edits it, runs it, and schedules it, administrators aside.
  owner_email: string;
  status: string;
  enabled: boolean;
  version: number;
  // category files the script under one lowercase slug, the axis the listings
  // filter on (#1369). Empty for a script nobody has filed.
  category?: string;
  tags?: string[];
  updated_at: string;
}

// ScriptVersion is one immutable snapshot with its author and the roles they
// held, which are the roles a run of this version presents.
export interface ScriptVersion {
  id: string;
  script_id: string;
  version: number;
  display_name: string;
  description: string;
  category?: string;
  source: string;
  tags?: string[];
  author: string;
  author_roles?: string[];
  status: string;
  created_at: string;
}

// ReferencedCapabilities is what a static read of the source found.
export interface ReferencedCapabilities {
  capabilities: string[];
  connections: string[];
  // tools are the tool names the source passes to platform.call literally. The
  // persona filter decides what a run may call; this is what it does call.
  tools?: string[];
  // destinations are where this script's OUTPUTS go: the names platform.export
  // writes to, counting the portal for any export that names none, because
  // that is where such an export lands. Not every byte the script can move — a
  // write made through platform.call is read in tools instead.
  destinations: string[];
  // refresh_targets are the output names platform.publish_data refreshes, so a
  // reader sees which asset's data region this script rewrites.
  refresh_targets?: string[];
  // dynamic_connections is true when a call computes its connection instead of
  // naming one, dynamic_destinations when one computes its destination, and
  // dynamic_refresh_targets when a publish_data call computes the name it
  // refreshes. Any of them makes that list incomplete.
  dynamic_connections: boolean;
  dynamic_destinations: boolean;
  dynamic_refresh_targets?: boolean;
  // dynamic_tools is true when a call computes the tool it invokes instead of
  // naming one, which makes the tool list incomplete.
  dynamic_tools?: boolean;
}

// ScriptFinding is one validator complaint about the source. The hint is the
// corrective action, which is most of a finding's value.
export interface ScriptFinding {
  severity: string;
  message: string;
  line?: number;
  hint?: string;
}

// ScriptDryRunOutput is one output a draft run would have written: the shape,
// and nothing else. A preview has no asset id and no object key, because it
// wrote neither.
export interface ScriptDryRunOutput {
  name: string;
  destination?: string;
  format: string;
  row_count: number;
  // A document was written verbatim from a string body, so its row count is
  // not a fact about it and is not shown.
  document?: boolean;
  // A refresh replaced the data region of an existing asset, and bytes is the
  // payload it would splice in.
  refresh?: boolean;
  bytes: number;
}

// ScriptDryRunAccount is the record of somebody having executed this exact
// source (#1364).
export interface ScriptDryRunAccount {
  id: string;
  script_id: string;
  requested_by?: string;
  status: string;
  error?: string;
  log?: string;
  log_truncated?: boolean;
  metrics: { steps: number; duration_ms: number; queries: number; exports: number };
  outputs?: ScriptDryRunOutput[];
  created_at: string;
}

// VersionDetail is everything the version surface shows for one version.
export interface VersionDetail {
  version: ScriptVersion;
  referenced: ReferencedCapabilities;
  findings?: ScriptFinding[];
  // dry_run is the account of this exact source having been run, absent when
  // nobody has run it.
  dry_run?: ScriptDryRunAccount;
}
