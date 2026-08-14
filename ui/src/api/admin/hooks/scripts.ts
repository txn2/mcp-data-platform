import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type {
  PendingReview,
  Script,
  ScriptApproveInput,
  ScriptVersion,
  VersionReview,
} from "../types/scripts";
import { apiFetch } from "../client";

// Managed-script review hooks (#1287). The queue, one script's history, one
// version's review payload, and the two decisions a reviewer can make.

// scriptsKey is the root of every script query key, so one invalidation after
// a decision refreshes the queue, the listing, and the open review together —
// approving changes all three.
const scriptsKey = ["admin", "scripts"] as const;

interface ListResponse<T> {
  data: T[];
  total: number;
}

export function useScriptReviews() {
  return useQuery({
    queryKey: [...scriptsKey, "reviews"],
    queryFn: () => apiFetch<ListResponse<PendingReview>>("/scripts/reviews"),
  });
}

export function useAdminScripts() {
  return useQuery({
    queryKey: [...scriptsKey, "list"],
    queryFn: () => apiFetch<ListResponse<Script>>("/scripts"),
  });
}

export function useScriptVersions(scriptID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "versions"],
    queryFn: () =>
      apiFetch<ListResponse<ScriptVersion>>(`/scripts/${scriptID}/versions`),
    enabled: !!scriptID,
  });
}

export function useScriptVersionReview(scriptID: string | null, version: number | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "versions", version],
    queryFn: () =>
      apiFetch<VersionReview>(`/scripts/${scriptID}/versions/${version}`),
    enabled: !!scriptID && !!version,
  });
}

// ScriptDecision names the version a decision applies to. Both mutations carry
// it because the surface can act on any version of a script, not only the one
// currently open: approving an earlier version is a rollback, and the store
// supports it.
export interface ScriptDecision {
  scriptID: string;
  version: number;
}

export function useApproveScriptVersion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      scriptID,
      version,
      grant,
    }: ScriptDecision & { grant: ScriptApproveInput }) =>
      apiFetch<ScriptVersion>(`/scripts/${scriptID}/versions/${version}/approve`, {
        method: "POST",
        body: JSON.stringify(grant),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: scriptsKey });
    },
  });
}

export function useRejectScriptVersion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ scriptID, version }: ScriptDecision) =>
      apiFetch<{ status: string }>(`/scripts/${scriptID}/versions/${version}/reject`, {
        method: "POST",
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: scriptsKey });
    },
  });
}
