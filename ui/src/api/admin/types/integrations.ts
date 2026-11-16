// Integrations (#2659): an operation only a deployment's environment knows how
// to do, handed to a script an administrator chooses. Site routing is the
// first. Mirrors internal/platform/integration.

export interface IntegrationBinding {
  id: string;
  operation: string;
  script_id: string;
  interval_seconds: number;
  current_run_id?: string;
  retry_requested: boolean;
  last_started_at?: string;
  last_run_id?: string;
  /** succeeded, failed, canceled; "" before the first run ends. */
  last_status?: string;
  /** change or timer. */
  last_reason?: string;
  /** The last run's error: what the script raised, or why it could not run. Administrators only. */
  last_error?: string;
  last_finished_at?: string;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
}

export interface IntegrationItem {
  key: string;
  entry: Record<string, unknown>;
  status: "pending" | "ready" | "failed";
  message: string;
  failed_run?: string;
  checked_at?: string;
}

export interface IntegrationOverview {
  operation: string;
  label: string;
  /** The function the script defines, called with req. */
  function: string;
  items_field: string;
  key_field: string;
  binding?: IntegrationBinding;
  script_name?: string;
  running_version?: number;
  approved_by?: string;
  counts: Partial<Record<IntegrationItem["status"], number>>;
  items: IntegrationItem[];
  /** How many items the operation has now, whether or not a script is chosen. */
  total: number;
}

export interface IntegrationCandidate {
  id: string;
  name: string;
  owner_email: string;
  version: number;
  /** The version approved for handlers, 0 for none. */
  running_version: number;
  approved_by?: string;
  /** Whether the approved version defines the function too. */
  running_serves: boolean;
}

export interface IntegrationBindInput {
  script_id: string;
  interval_seconds?: number;
  approve?: boolean;
}
