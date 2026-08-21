import { useQuery } from "@tanstack/react-query";
import type { Script, ScriptVersion, VersionDetail } from "../types/scripts";
import { apiFetch } from "../client";

// Managed-script admin hooks: the listing, one script's history, one version's
// detail payload, and the operator's cross-script run listing.

// scriptsKey is the root of every script query key.
const scriptsKey = ["admin", "scripts"] as const;

interface ListResponse<T> {
  data: T[];
  total: number;
}

export function useAdminScripts() {
  return useQuery({
    queryKey: [...scriptsKey, "list"],
    queryFn: () => apiFetch<ListResponse<Script>>("/scripts"),
  });
}

// AdminScriptRun is one run in the operator's cross-script listing. It carries
// the script id because a listing across scripts is unreadable without it; the
// page resolves the id to a name from the script listing it already holds.
export interface AdminScriptRun {
  id: string;
  script_id: string;
  status: string;
  trigger: string;
  version: number;
  fire_time: string;
  started_at?: string;
  finished_at?: string;
  duration_ms: number;
  error?: string;
  output_count: number;
  requested_by?: string;
}

// AdminRunListResponse carries the cap the listing was read under, so a page
// that filled it can say there is older history behind it rather than implying
// it showed everything.
interface AdminRunListResponse extends ListResponse<AdminScriptRun> {
  limit: number;
}

// useAdminScriptRuns reads recent runs across every script — the operator's
// view of what the platform has been running unattended (#1307).
export function useAdminScriptRuns() {
  return useQuery({
    queryKey: [...scriptsKey, "runs"],
    queryFn: () => apiFetch<AdminRunListResponse>("/scripts/runs"),
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

export function useScriptVersionDetail(scriptID: string | null, version: number | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "versions", version],
    queryFn: () =>
      apiFetch<VersionDetail>(`/scripts/${scriptID}/versions/${version}`),
    enabled: !!scriptID && !!version,
  });
}
