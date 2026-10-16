// A connection's scratch schema reconciled against its table registrations
// (#2136), as /api/v1/admin/tables/reconcile returns it.

/** The registration fields the reconcile view reads. */
export interface ReconcileRegistration {
  id: string;
  source_kind: "resource" | "asset" | "webhook";
  source_id: string;
  connection: string;
  catalog: string;
  schema: string;
  table: string;
  location: string;
  registered_by: string;
  registered_at: string;
}

export interface ReconcileOrphan {
  table: string;
  location: string;
  /** The stored file the location is the directory of, when it is one. */
  source?: { kind: "resource" | "asset"; id: string; exists: boolean };
  /** Whether the platform can show the table is its own; only those are repaired. */
  provable: boolean;
}

export interface ReconcileMissing {
  registration: ReconcileRegistration;
  provable: boolean;
}

export interface ReconcileDrifted {
  registration: ReconcileRegistration;
  location: string;
}

export interface Reconciliation {
  connection: string;
  catalog: string;
  schema: string;
  orphans: ReconcileOrphan[];
  missing: ReconcileMissing[];
  drifted: ReconcileDrifted[];
  /** The schema held more tables than one listing returns. */
  truncated: boolean;
}

export interface RepairItem {
  action: "drop" | "forget";
  table?: string;
  registration_id?: string;
}

export interface RepairOutcome extends RepairItem {
  done: boolean;
  reason?: string;
}
