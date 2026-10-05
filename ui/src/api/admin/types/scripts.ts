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
  // library is true for a library (#1941): a script with no main() that other
  // scripts load as load("lib:<name>@<version>", ...) and that is never run
  // or scheduled itself. loads is the library versions this script's current
  // source loads, as "<name>@<version>". The server always sends both; they
  // are optional here because an older server sends neither.
  library?: boolean;
  loads?: string[];
  // exclusive is the owner's setting that the script runs one at a time
  // (#1986). Optional for a server that predates it.
  exclusive?: boolean;
  updated_at: string;
}

// LibraryUse is one script whose current source loads a library (#1941), and
// the version of the library it loads.
export interface LibraryUse {
  script_id: string;
  name: string;
  display_name: string;
  owner_email: string;
  version: number;
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
  // change_summary is what this version does differently from the one before,
  // in plain language, agreed with the person the automation runs for (#1942):
  // change_agreed_by confirmed that agreement at change_agreed_at. All three
  // are absent on a version that changed no behavior.
  change_summary?: string;
  change_agreed_by?: string;
  change_agreed_at?: string;
  // tests is what the version's tests found when it was saved (#1972),
  // absent on a version saved before reports were kept.
  tests?: ScriptVersionTests;
}

// ScriptVersionTests is a saved version's test report.
export interface ScriptVersionTests {
  tests: { name: string; passed: boolean; line?: number; failure?: string }[];
  passed: number;
  failed: number;
  coverage: { statements: number; covered: number; percent: number; missed_lines: number[] };
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
  // rule names the authoring gate a finding comes from (#1913), absent for
  // the validator's own findings.
  rule?: string;
  severity: string;
  message: string;
  line?: number;
  hint?: string;
}

// ScriptDryRunOutput is one output of a draft run. A preview is the shape and
// nothing else: it has no asset id and no object key, because it wrote
// neither. A draft run with allow_writes writes its outputs (#1822), and then
// says so and where.
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
  // written marks an output the draft wrote for real; reference names the
  // resource or asset it wrote, and table the table a register= argument made
  // over it (#1820).
  written?: boolean;
  reference?: string;
  table?: string;
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
  metrics: {
    steps: number;
    duration_ms: number;
    queries: number;
    exports: number;
  };
  outputs?: ScriptDryRunOutput[];
  // state_written is the state the draft would have saved, absent when the
  // source saved none; a draft persists it no more than an output.
  state_written?: Record<string, unknown>;
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
