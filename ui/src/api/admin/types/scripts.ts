// Managed-script review types (#1287). They mirror the admin REST payloads
// served by internal/httpserver/scripthttp.

// ScriptGrants is the capability set bound to an approved version: what that
// code may reach when the platform runs it with nobody present.
//
// Roles are read-only everywhere in this UI. Approval copies them from the
// version's author, so offering to edit them would imply a control the API
// does not have.
export interface ScriptGrants {
  roles: string[];
  connections: string[];
  capabilities: string[];
  destinations: ScriptDestination[];
}

// ScriptDestination is one place an approved version may write. It carries the
// address, not only a name: a grant that named "acme-drop" alone would leave
// what that means in configuration a reviewer cannot see, and an operator could
// repoint it afterwards without anyone approving anything.
export interface ScriptDestination {
  name: string;
  kind: string;
  connection?: string;
  bucket?: string;
  prefix?: string;
}

// Script is a managed script's live row.
export interface Script {
  id: string;
  name: string;
  display_name: string;
  description: string;
  scope: string;
  owner_email: string;
  status: string;
  enabled: boolean;
  version: number;
  // approved_version_id is the execution gate: empty means nothing of this
  // script runs.
  approved_version_id?: string;
  tags?: string[];
  updated_at: string;
}

// ScriptVersion is one immutable snapshot with its approval stamp.
export interface ScriptVersion {
  id: string;
  script_id: string;
  version: number;
  display_name: string;
  description: string;
  source: string;
  tags?: string[];
  author: string;
  // author_roles is the authority approving this version binds.
  author_roles?: string[];
  status: string;
  approved_by?: string;
  approved_at?: string;
  grants: ScriptGrants;
  created_at: string;
}

// PendingReview is one row of the review queue: a version waiting for a
// decision, with the script it belongs to.
export interface PendingReview {
  script_id: string;
  script_name: string;
  display_name: string;
  description: string;
  owner_email: string;
  scope: string;
  version: number;
  version_id: string;
  version_status: string;
  author: string;
  author_roles: string[];
  // first_approval marks a script that has never had an approved version:
  // approving starts something running rather than changing what runs.
  first_approval: boolean;
  created_at: string;
}

// ReferencedCapabilities is what a static read of the source found.
export interface ReferencedCapabilities {
  capabilities: string[];
  connections: string[];
  // destinations are the destination names the source writes to, counting the
  // portal for any export that names none, because that is where such an
  // export lands.
  destinations: string[];
  // dynamic_connections is true when a call computes its connection instead of
  // naming one, and dynamic_destinations when one computes its destination.
  // Either makes that list incomplete.
  dynamic_connections: boolean;
  dynamic_destinations: boolean;
}

// ScriptFinding is one validator complaint about the source. The hint is the
// corrective action, which is most of a finding's value to a reviewer deciding
// whether to send a version back.
export interface ScriptFinding {
  severity: string;
  message: string;
  line?: number;
  hint?: string;
}

// ApprovedBaseline is the version the script executes today: the other half of
// both diffs a reviewer reads. Absent when nothing has ever been approved.
export interface ApprovedBaseline {
  version: number;
  version_id: string;
  grants: ScriptGrants;
  approved_by?: string;
  approved_at?: string;
  // source_diff is the unified diff from the approved source to this one. It
  // is empty when the two carry identical source, which is what re-approving a
  // version to change its grant looks like.
  source_diff?: string;
}

// ScriptDryRunOutput is one output a draft run would have written: the shape,
// and nothing else. A preview has no asset id and no object key, because it
// wrote neither.
export interface ScriptDryRunOutput {
  name: string;
  destination?: string;
  format: string;
  row_count: number;
  bytes: number;
}

// ScriptDryRunAccount is the record of somebody having executed this exact
// source (#1364). Its absence on a version is itself the answer a reviewer
// needs: nobody ran the code they are being asked to approve.
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

// VersionReview is everything the review surface shows for one version.
export interface VersionReview {
  version: ScriptVersion;
  referenced: ReferencedCapabilities;
  missing_capabilities?: string[];
  missing_connections?: string[];
  missing_destinations?: string[];
  findings?: ScriptFinding[];
  approved?: ApprovedBaseline;
  // dry_run is the account of this exact source having been run, absent when
  // nobody has run it.
  dry_run?: ScriptDryRunAccount;
}

// ScriptApproveInput is the approval body. It carries no roles: see
// ScriptGrants.
export interface ScriptApproveInput {
  connections: string[];
  capabilities: string[];
  destinations: ScriptDestination[];
}
