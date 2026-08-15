import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Script, ScriptVersion } from "@/api/admin/types";
import { apiFetch } from "../client";

// Portal script hooks (#1290): the read surface for the people who own the
// automations. Every route here is read-only — approving a version is an
// administrator's action and lives on the admin API.

// ScriptSchedule is a script's cadence as the portal reports it.
export interface ScriptSchedule {
  id: string;
  script_id: string;
  cron_spec: string;
  timezone: string;
  enabled: boolean;
  next_run_at?: string;
  last_fire_at?: string;
  missed_fires?: number;
}

// ScriptRun is one run as a listing reports it. The log is absent by design:
// it is read one run at a time, through useScriptRun.
export interface ScriptRun {
  id: string;
  status: string;
  trigger: string;
  version: number;
  fire_time: string;
  started_at?: string;
  finished_at?: string;
  duration_ms: number;
  error?: string;
  // output_count is how many outputs the run persisted; the run detail carries
  // the outputs themselves.
  output_count: number;
  requested_by?: string;
}

// ScriptRunOutput is one thing a run persisted, as the run record carries it:
// a versioned portal asset, or an object delivered to a bucket. The two are
// different kinds of answer — the platform still serves the asset and can link
// to it, while the delivered object left the platform and cannot be fetched
// back. An empty destination is the portal, which is where an output that
// named none has always landed.
export interface ScriptRunOutput {
  name: string;
  destination?: string;
  asset_id?: string;
  asset_version?: number;
  bucket?: string;
  key?: string;
  format: string;
  row_count: number;
  bytes: number;
}

// ScriptContractOutput is the same output as the contract document reports it,
// which names its kind rather than leaving a reader to infer it from which
// locator happens to be filled in.
export interface ScriptContractOutput {
  name: string;
  kind: string;
  destination: string;
  format?: string;
  row_count?: number;
  bytes?: number;
  asset_id?: string;
  asset_version?: number;
  bucket?: string;
  key?: string;
}

// ScriptRunDetail is one run in full, as the run record itself: its bound
// parameters, what it cost, what it wrote, and the bounded log it captured.
export interface ScriptRunDetail extends ScriptRun {
  script_id: string;
  scheduled_for: string;
  attempt: number;
  params?: Record<string, unknown>;
  log?: string;
  log_truncated?: boolean;
  metrics: { steps: number; duration_ms: number; queries: number; exports: number };
  outputs?: ScriptRunOutput[];
  created_at: string;
}

// ScriptParam is one declared parameter of the version a run binds against.
export interface ScriptParam {
  name: string;
  type: string;
  description?: string;
  required: boolean;
  default?: unknown;
  values?: string[];
}

// ScriptContractApproval is the execution gate as a reader sees it: whether a
// version is approved, and whether a run requested right now would be admitted
// at all. The refusal is the gate's own answer, so a page never reports a
// script as runnable that run_script would decline.
export interface ScriptContractApproval {
  approved: boolean;
  version?: number;
  approved_by?: string;
  approved_at?: string;
  refusal?: string;
}

// ScriptContractRun is what the script last successfully produced.
export interface ScriptContractRun {
  run_id: string;
  version: number;
  finished_at?: string;
  outputs?: ScriptContractOutput[];
}

// ScriptContract is the one document a script resolves to, shared with fetch
// and with prompt serve.
export interface ScriptContract {
  id: string;
  name: string;
  display_name?: string;
  description?: string;
  owner_email?: string;
  scope: string;
  personas?: string[];
  tags?: string[];
  status: string;
  enabled: boolean;
  params: ScriptParam[];
  approval: ScriptContractApproval;
  schedule?: { cron_spec: string; timezone: string; enabled: boolean; next_run_at?: string };
  last_successful_run?: ScriptContractRun;
}

// PortalScriptRow is one script in the portal listing.
export interface PortalScriptRow {
  script: Script;
  schedule?: ScriptSchedule;
  // last_run is present only for the scripts this caller owns: a run is
  // owner-and-admin reading, and so is the fact that one failed.
  last_run?: ScriptRun;
  // owned reports whether this caller may read the script's runs, source, and
  // grant, so the page offers those surfaces rather than linking to a refusal.
  owned: boolean;
}

interface ListResponse<T> {
  data: T[];
  total: number;
}

// scriptsKey roots every portal script query, so one invalidation refreshes
// the listing and any open detail together.
const scriptsKey = ["portal", "scripts"] as const;

export function useMyScripts() {
  return useQuery({
    queryKey: [...scriptsKey, "list"],
    queryFn: () => apiFetch<ListResponse<PortalScriptRow>>("/scripts"),
  });
}

export function useScriptContract(scriptID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "contract"],
    queryFn: () => apiFetch<{ contract: ScriptContract; owned: boolean }>(`/scripts/${scriptID}`),
    enabled: !!scriptID,
  });
}

// usePortalScriptVersions reads a script's version history. It is owner and
// admin reading, so it is requested only when the listing said the caller owns
// the script; a caller who does not is answered as though it did not exist.
export function usePortalScriptVersions(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "versions"],
    queryFn: () => apiFetch<ListResponse<ScriptVersion>>(`/scripts/${scriptID}/versions`),
    enabled: !!scriptID && owned,
  });
}

// RUN_PAGE_SIZE is how many runs the history asks for. The page states when a
// result fills it, so a script that runs every half hour never reads as though
// its history began this morning.
export const RUN_PAGE_SIZE = 25;

export function useScriptRuns(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", RUN_PAGE_SIZE],
    queryFn: () =>
      apiFetch<ListResponse<ScriptRun>>(`/scripts/${scriptID}/runs?per_page=${RUN_PAGE_SIZE}`),
    enabled: !!scriptID && owned,
  });
}

export function useScriptRun(scriptID: string | null, runID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", runID],
    queryFn: () => apiFetch<ScriptRunDetail>(`/scripts/${scriptID}/runs/${runID}`),
    enabled: !!scriptID && !!runID,
  });
}

// ScheduleInput is the cadence an owner sets: when it runs, in which zone, and
// the parameter values every fire binds.
export interface ScheduleInput {
  cron: string;
  timezone: string;
  params?: Record<string, unknown>;
  enabled?: boolean;
}

// useSetScriptSchedule replaces a script's cadence. A cadence carries no
// authority — the execution gate and the capability grant are read again at
// every fire — so it is the owner's to set, and this is the only mutation the
// portal's script surface has.
export function useSetScriptSchedule(scriptID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ScheduleInput) =>
      apiFetch<ScriptSchedule>(`/scripts/${scriptID}/schedule`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: scriptsKey });
    },
  });
}

// useSetScriptScheduleEnabled pauses or resumes a schedule. It is a separate
// action from setting the cadence because sending the whole cadence back to
// turn it off would re-base the next fire, and a paused schedule must resume on
// the fire it was parked on.
export function useSetScriptScheduleEnabled(scriptID: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      apiFetch<ScriptSchedule>(
        `/scripts/${scriptID}/schedule/${enabled ? "enable" : "disable"}`,
        { method: "POST" },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: scriptsKey });
    },
  });
}
