import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { IntegrationBinding, IntegrationBindInput, IntegrationCandidate, IntegrationOverview } from "../types";
import { REFETCH_INTERVAL } from "./shared";

// Integrations (#2659): Admin > Settings > Integrations.

const KEY = ["integrations"] as const;

/** useIntegrations reads every operation, refreshed so a run's outcome shows without a reload. */
export function useIntegrations() {
  return useQuery({
    queryKey: KEY,
    queryFn: () => apiFetch<{ integrations: IntegrationOverview[] }>("/integrations"),
    refetchInterval: REFETCH_INTERVAL,
  });
}

/** useIntegrationScripts reads the scripts whose latest version defines the operation's function. */
export function useIntegrationScripts(operation: string, search: string, enabled: boolean) {
  const params = new URLSearchParams(search ? { search } : {});
  return useQuery({
    queryKey: [...KEY, operation, "scripts", search],
    queryFn: () => apiFetch<{ scripts: IntegrationCandidate[] }>(`/integrations/${encodeURIComponent(operation)}/scripts?${params.toString()}`),
    enabled,
  });
}

export function useBindIntegration(operation: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: IntegrationBindInput) =>
      apiFetch<IntegrationBinding>(`/integrations/${encodeURIComponent(operation)}`, { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  });
}

export function useClearIntegration(operation: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<{ done: boolean }>(`/integrations/${encodeURIComponent(operation)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  });
}

export function useRetryIntegration(operation: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<{ done: boolean }>(`/integrations/${encodeURIComponent(operation)}/retry`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  });
}
