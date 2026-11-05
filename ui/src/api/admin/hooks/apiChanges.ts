import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// Keeping API catalog specs current (#2469): how a spec is checked and whether
// a change waits for review (#2483), an uploaded spec's upstream URL (#2484),
// what uses an operation (#2485), and what upstreams said about a connection's
// operations (#2486). The shapes mirror internal/apichange and
// internal/admin/apichangeapi.

/** APISpecWatch is how one spec is kept current. */
export interface APISpecWatch {
  /** A Go duration ("168h0m0s"), "" when the spec is checked on request only. */
  refresh_interval: string;
  hold_changes: boolean;
  upstream_url: string;
  next_check_at?: string;
  last_checked_at?: string;
  last_error?: string;
  /** The id of the change waiting for review, if any. */
  pending_change?: string;
  updated_by?: string;
}

export interface APISpecWatchInput {
  refresh_interval: string;
  hold_changes: boolean;
  upstream_url: string;
}

/** APICaller is one script, endpoint or prompt that uses an operation. */
export interface APICaller {
  kind: "script" | "endpoint" | "prompt";
  id: string;
  name: string;
  /** source: the saved source names it; run: a run called it; prompt: its text names it. */
  via: "source" | "run" | "prompt";
  connection?: string;
  last_seen_at?: string;
}

export interface APIChangeOp {
  id: string;
  method: string;
  path: string;
  summary?: string;
  used_by?: APICaller[];
}

export interface APIChangedOp extends APIChangeOp {
  parts: string[];
}

export interface APIChangeReport {
  from_version?: string;
  to_version?: string;
  added: APIChangeOp[];
  removed: APIChangeOp[];
  changed: APIChangedOp[];
  deprecated: APIChangeOp[];
  truncated?: boolean;
}

export type APIChangeStatus = "applied" | "pending" | "discarded" | "reported" | "superseded";

export interface APIChange {
  id: string;
  catalog_id: string;
  spec_name: string;
  status: APIChangeStatus;
  trigger: "schedule" | "manual";
  report: APIChangeReport;
  /** "3 added, 1 removed, 2 changed, 1 deprecated", with a version change. */
  summary: string;
  created_at: string;
  decided_by?: string;
  decided_at?: string;
}

/** APIRefreshResult is what a refresh did. */
export interface APIRefreshResult {
  spec_name: string;
  source_kind: string;
  etag?: string;
  last_fetched_at?: string;
  operation_count: number;
  not_modified: boolean;
  change?: APIChange;
}

export interface APIOperationUsedBy {
  operation: string;
  used_by: APICaller[];
}

export interface APIConnectionUsedBy {
  connection: string;
  catalog_id?: string;
  operations: APIOperationUsedBy[];
}

export interface APIUpstreamWarning {
  connection: string;
  operation: string;
  kind: "deprecation" | "sunset" | "removed";
  detail?: string;
  first_seen_at: string;
  last_seen_at: string;
  seen_count: number;
  notified_at?: string;
}

export interface APIChangeSettings {
  channels: string[];
}

const specPath = (catalogID: string, specName: string) =>
  `/api-catalogs/${encodeURIComponent(catalogID)}/specs/${encodeURIComponent(specName)}`;

const watchKey = (catalogID: string, specName: string) =>
  ["api-catalogs", catalogID, "specs", specName, "watch"] as const;
const changesKey = (catalogID: string, specName: string) =>
  ["api-catalogs", catalogID, "specs", specName, "changes"] as const;
const SETTINGS_KEY = ["settings", "api-changes"] as const;

export function useAPISpecWatch(catalogID: string, specName: string, enabled = true) {
  return useQuery({
    queryKey: watchKey(catalogID, specName),
    queryFn: () => apiFetch<APISpecWatch>(`${specPath(catalogID, specName)}/watch`),
    enabled: enabled && !!catalogID && !!specName,
  });
}

export function useSaveAPISpecWatch() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ catalogID, specName, input }: { catalogID: string; specName: string; input: APISpecWatchInput }) =>
      apiFetch<APISpecWatch>(`${specPath(catalogID, specName)}/watch`, {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    onSuccess: (data, vars) => {
      qc.setQueryData(watchKey(vars.catalogID, vars.specName), data);
    },
  });
}

export function useAPISpecChanges(catalogID: string, specName: string, enabled = true) {
  return useQuery({
    queryKey: changesKey(catalogID, specName),
    queryFn: () => apiFetch<{ changes: APIChange[] }>(`${specPath(catalogID, specName)}/changes`),
    enabled: enabled && !!catalogID && !!specName,
  });
}

/** useDecideAPIChange applies or discards a change waiting for review. */
export function useDecideAPIChange() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      catalogID,
      specName,
      changeID,
      decision,
    }: {
      catalogID: string;
      specName: string;
      changeID: string;
      decision: "apply" | "discard";
    }) =>
      apiFetch<unknown>(
        `${specPath(catalogID, specName)}/changes/${encodeURIComponent(changeID)}/${decision}`,
        { method: "POST" },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs", vars.catalogID] });
    },
  });
}

export function useOperationUsedBy(catalogID: string, specName: string, operationID: string, enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs", catalogID, "specs", specName, "operations", operationID, "used-by"],
    queryFn: () =>
      apiFetch<APIOperationUsedBy>(
        `${specPath(catalogID, specName)}/operations/${encodeURIComponent(operationID)}/used-by`,
      ),
    enabled: enabled && !!catalogID && !!specName && !!operationID,
  });
}

export function useConnectionUsedBy(connection: string) {
  return useQuery({
    queryKey: ["connection-instances", "api", connection, "used-by"],
    queryFn: () =>
      apiFetch<APIConnectionUsedBy>(`/connection-instances/api/${encodeURIComponent(connection)}/used-by`),
    enabled: !!connection,
  });
}

export function useConnectionWarnings(connection: string) {
  return useQuery({
    queryKey: ["connection-instances", "api", connection, "warnings"],
    queryFn: () =>
      apiFetch<{ connection: string; warnings: APIUpstreamWarning[] }>(
        `/connection-instances/api/${encodeURIComponent(connection)}/warnings`,
      ),
    enabled: !!connection,
  });
}

export function useAPIChangeSettings() {
  return useQuery({
    queryKey: SETTINGS_KEY,
    queryFn: () => apiFetch<APIChangeSettings>("/settings/api-changes"),
  });
}

export function useSaveAPIChangeSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: APIChangeSettings) =>
      apiFetch<APIChangeSettings>("/settings/api-changes", { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => {
      qc.setQueryData(SETTINGS_KEY, data);
    },
  });
}
