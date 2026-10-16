import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { Reconciliation, RepairItem, RepairOutcome } from "../types";

// The scratch-schema reconcile (#2136).

const KEY = ["table-reconcile"] as const;

/** useReconciliation reads one connection's scratch schema against its
 * registrations. Nothing is read until a connection is chosen. */
export function useReconciliation(connection: string) {
  return useQuery({
    queryKey: [...KEY, connection],
    queryFn: () =>
      apiFetch<Reconciliation>(`/tables/reconcile?connection=${encodeURIComponent(connection)}`),
    enabled: !!connection,
  });
}

/** useRepairTables carries out repairs on a connection and reads it again. */
export function useRepairTables() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { connection: string; repair: RepairItem[] }) =>
      apiFetch<{ outcomes: RepairOutcome[] }>("/tables/reconcile", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
      void qc.invalidateQueries({ queryKey: ["scratch-tables"] });
    },
  });
}
