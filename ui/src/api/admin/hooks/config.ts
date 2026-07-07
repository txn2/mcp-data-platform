import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";

// ---------------------------------------------------------------------------
// API Keys
// ---------------------------------------------------------------------------

export function useAPIKeys() {
  return useQuery({
    queryKey: ["auth", "keys"],
    queryFn: () => apiFetch<import("../types").APIKeyListResponse>("/auth/keys"),
  });
}

export function useCreateAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; email?: string; description?: string; roles: string[]; expires_in?: string }) =>
      apiFetch<import("../types").APIKeyCreateResponse>("/auth/keys", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["auth", "keys"] });
    },
  });
}

export function useDeleteAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetchRaw(`/auth/keys/${name}`, { method: "DELETE" }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["auth", "keys"] });
    },
  });
}

// --- Config entries ---

export function useConfigEntries() {
  return useQuery({
    queryKey: ["config", "entries"],
    queryFn: () => apiFetch<import("../types").ConfigEntry[]>("/config/entries"),
  });
}

export function useConfigEntry(key: string) {
  return useQuery({
    queryKey: ["config", "entries", key],
    queryFn: () => apiFetch<import("../types").ConfigEntry>(`/config/entries/${key}`),
    enabled: !!key,
    retry: false,
  });
}

export function useSetConfigEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, value }: { key: string; value: string }) =>
      apiFetch<import("../types").ConfigEntry>(`/config/entries/${key}`, {
        method: "PUT",
        body: JSON.stringify({ value }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["config", "entries"] });
      void qc.invalidateQueries({ queryKey: ["config", "effective"] });
      void qc.invalidateQueries({ queryKey: ["system", "info"] });
    },
  });
}

export function useDeleteConfigEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (key: string) =>
      apiFetchRaw(`/config/entries/${key}`, { method: "DELETE" }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["config", "entries"] });
      void qc.invalidateQueries({ queryKey: ["config", "effective"] });
      void qc.invalidateQueries({ queryKey: ["system", "info"] });
    },
  });
}

export function useConfigChangelog() {
  return useQuery({
    queryKey: ["config", "changelog"],
    queryFn: () => apiFetch<import("../types").ConfigChangelogEntry[]>("/config/changelog"),
  });
}

export function useEffectiveConfig() {
  return useQuery({
    queryKey: ["config", "effective"],
    queryFn: () => apiFetch<import("../types").EffectiveConfigEntry[]>("/config/effective"),
  });
}

// useAgentInstructionsBaseline fetches the platform-owned instruction baseline
// (#646): the "how to operate" guidance composed beneath the admin's
// agent_instructions, naming only tools this deployment exposes.
export function useAgentInstructionsBaseline() {
  return useQuery({
    queryKey: ["config", "agent-instructions-baseline"],
    queryFn: () =>
      apiFetch<import("../types").AgentInstructionsBaseline>(
        "/config/agent-instructions-baseline",
      ),
  });
}

export function useEffectiveConnections() {
  return useQuery({
    queryKey: ["connection-instances", "effective"],
    queryFn: () => apiFetch<import("../types").EffectiveConnection[]>("/connection-instances/effective"),
    // Poll so the gateway reachability badge reflects an upstream going
    // down or recovering at runtime, matching the cadence of the adjacent
    // OAuth-health badge (useConnectionsOAuthHealth). Without this the
    // badge would be static after load (the query is otherwise only
    // invalidated by connection create/delete), defeating its purpose of
    // surfacing a dead upstream from the list. A background refetch does
    // not clobber an in-progress edit: the editor holds its own form
    // state and only reads the connection for dirty comparison.
    refetchInterval: 10000,
  });
}
