import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  Script,
  ScriptDryRunOutput,
  ScriptFinding,
  ScriptVersion,
} from "@/api/admin/types";
import { ApiError, apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// Portal script hooks (#1290): the surface for the people who own the
// scripts. Saving a version makes it the version that runs, and the
// cadence an owner sets here (#1307) carries no authority at all.

// ScriptSchedule is a script's cadence as the portal reports it.
export interface ScriptSchedule {
  id: string;
  script_id: string;
  cron_spec: string;
  timezone: string;
  enabled: boolean;
  // params are the values every fire binds, with ${fire_date} stored as
  // written: it expands at the fire, so a run records the date it computed.
  params?: Record<string, unknown>;
  next_run_at?: string;
  last_fire_at?: string;
  missed_fires?: number;
  created_by?: string;
  updated_by?: string;
}

// ScriptScheduleInput is a cadence as its owner submits it. It carries no
// roles, connections, or capabilities, and could not usefully: every fire
// executes the latest saved version, authorized against the roles captured at
// that save.
export interface ScriptScheduleInput {
  cron: string;
  timezone?: string;
  params?: Record<string, unknown>;
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
  // A document was written verbatim from a string body, so its row count is
  // not a fact about it and is not shown.
  document?: boolean;
  // A refresh replaced the data region of an existing asset; bytes is the
  // payload spliced in, not the document.
  refresh?: boolean;
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
  // A refresh replaced the data region of an existing asset rather than
  // writing a whole document.
  refresh?: boolean;
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
  // The state the run read at creation (an input beside its parameters) and,
  // on a succeeded run that saved, what it wrote and the revision (#1537).
  state_revision?: number;
  state_read?: Record<string, unknown>;
  state_written?: Record<string, unknown>;
  state_revision_written?: number;
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
  // category files the script under one lowercase slug (#1369), empty for a
  // script nobody has filed.
  category?: string;
  tags?: string[];
  status: string;
  enabled: boolean;
  params: ScriptParam[];
  // version is the version a run executes: the latest saved one.
  version: number;
  // refusal is the run gate's own answer to "would a run be admitted now",
  // empty when one would be. A page never reports a script as runnable that
  // run_script would decline.
  refusal?: string;
  schedule?: { cron_spec: string; timezone: string; enabled: boolean; next_run_at?: string };
  last_successful_run?: ScriptContractRun;
  // state says whether this script carries anything between runs (#1537).
  state?: import("./scriptState").ScriptContractState;
}

// PortalScriptRow is one script in the portal listing.
export interface PortalScriptRow {
  script: Script;
  schedule?: ScriptSchedule;
  // last_run is present only for the scripts this caller owns: a run is
  // owner-and-admin reading, and so is the fact that one failed.
  last_run?: ScriptRun;
  // owned reports whether this caller may read the script's runs and source,
  // so the page offers those surfaces rather than linking to a refusal.
  owned: boolean;
}

interface ListResponse<T> {
  data: T[];
  total: number;
}

// The state hooks (#1537) live in scriptState.ts and share the query root.
export {
  useClearScriptState,
  useScriptState,
  useSetScriptState,
  type ScriptContractState,
  type ScriptState,
} from "./scriptState";

// ScriptListFilter narrows the listing to one category, one tag, free text, or
// any combination (#1369, #1405). Every axis is applied by the server rather
// than in the table, so the answer is the same one an agent's list gets, and a
// filtered page does not depend on having already loaded every row — the
// listing is capped, and a page that filtered its own rows would answer from a
// truncated set while reporting a count to match.
export interface ScriptListFilter {
  category?: string;
  tag?: string;
  /** search matches a script's name, display name, or description. */
  search?: string;
}

// useScriptListing reads the scripts this caller may see: their own, and every
// script on the platform for an administrator, which is what the
// administrator's section lists (#1407). One listing serves both surfaces
// because the server answers both from one predicate; a second endpoint for
// the admin page would be a second answer to the same question.
export function useScriptListing(filter: ScriptListFilter = {}) {
  const query = scriptListQuery(filter);
  return useQuery({
    queryKey: [...scriptsKey, "list", filter.category ?? "", filter.tag ?? "", filter.search ?? ""],
    queryFn: () => apiFetch<ListResponse<PortalScriptRow>>(`/scripts${query}`),
  });
}

// scriptListQuery renders the filter as a query string, empty when nothing is
// filtered so the unfiltered request stays the plain one.
export function scriptListQuery(filter: ScriptListFilter): string {
  const params = new URLSearchParams();
  if (filter.category) params.set("category", filter.category);
  if (filter.tag) params.set("tag", filter.tag);
  if (filter.search) params.set("search", filter.search);
  const rendered = params.toString();
  return rendered ? `?${rendered}` : "";
}

// PortalScriptRun is one run in the caller's cross-script listing (#1405): the
// run, plus which script it belongs to. The name is what the row reads as and
// the id is what its link is built from; a run whose script is outside the
// listing the server resolved names from carries no name, and the row shows
// the id.
export interface PortalScriptRun extends ScriptRun {
  script_id: string;
  script_name?: string;
}

// OwnRunsResponse carries the cap the answer was read under, so a page that
// filled it says there is older history behind it rather than presenting a
// truncated listing as the whole of it.
interface OwnRunsResponse extends ListResponse<PortalScriptRun> {
  limit: number;
}

// useScriptRunListing reads the caller's runs across every script they own,
// newest first, and every run on the platform for an administrator. It answers
// the question the per-script history cannot: how are my scripts going, all of
// them — which previously took opening each in turn.
//
// Naming a script narrows it back to that one (#1407), which is what a metric
// naming a script links to: the same listing, filtered by the server, so the
// row cap counts the runs of that script rather than of everything.
//
// It polls while anything is still in flight, on the same terms and for the
// same reason the per-script history does: a run asked for on a script's page
// is executed by a worker, so the outcome arrives after the request.
export function useScriptRunListing(scriptID?: string) {
  const query = scriptID ? `?script_id=${encodeURIComponent(scriptID)}` : "";
  return useQuery({
    queryKey: [...scriptsKey, "runs", scriptID ?? ""],
    queryFn: () => apiFetch<OwnRunsResponse>(`/scripts/runs${query}`),
    refetchInterval: (query) => (hasRunInFlight(query.state.data) ? RUN_POLL_MS : false),
  });
}

export function useScriptContract(scriptID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "contract"],
    // source is the live code, served only to the script's owner: it is what
    // the editor on that page opens (#1307). draft_params is the parameter
    // contract read beside it, so the dry-run form binds against exactly the
    // contract the code was written against.
    queryFn: () =>
      apiFetch<{
        contract: ScriptContract;
        owned: boolean;
        source?: string;
        draft_params?: ScriptParam[];
      }>(`/scripts/${scriptID}`),
    enabled: !!scriptID,
  });
}

// ScriptSourceOutcome is the saved edit: the live script now carries this
// source, and it is the version a run executes.
export interface ScriptSourceOutcome {
  applied: boolean;
  message: string;
}

// useSaveScriptSource saves new Starlark for a script. The saved version is
// what runs from here on.
export function useSaveScriptSource(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (source: string) =>
      apiFetch<ScriptSourceOutcome>(`/scripts/${scriptID}/source`, {
        method: "PUT",
        body: JSON.stringify({ source }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

// ScriptMetadataInput is a change to what a script says about itself. Every
// field is optional and an omitted one is left alone, so a form that edits one
// field cannot blank the others.
export interface ScriptMetadataInput {
  display_name?: string;
  description?: string;
  category?: string;
  tags?: string[];
}

// ScriptMetadataOutcome is the saved state, plus the non-blocking advisory that
// fires when a description has grown into a document of its own (#1369).
export interface ScriptMetadataOutcome {
  version: number;
  description_notice?: string;
  message: string;
}

// useSaveScriptMetadata saves the display name, description, category and tags
// of a script: what it SAYS about itself, not what it does. The change is
// still captured as a version.
export function useSaveScriptMetadata(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ScriptMetadataInput) =>
      apiFetch<ScriptMetadataOutcome>(`/scripts/${scriptID}/metadata`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

// ScriptOwnerOutcome is a completed transfer: where the script landed, the
// version the move recorded, and what it means for the next run.
export interface ScriptOwnerOutcome {
  owner_email: string;
  version: number;
  message: string;
}

// useTransferScriptOwner moves a script to another person. It is an
// administrator's action: ownership is the whole of what a script is, so
// handing it over hands over everything at once, and the run identity is
// re-captured from the administrator making the move.
export function useTransferScriptOwner(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (ownerEmail: string) =>
      apiFetch<ScriptOwnerOutcome>(`/scripts/${scriptID}/owner`, {
        method: "PUT",
        body: JSON.stringify({ owner_email: ownerEmail }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
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

// RUN_POLL_MS is how often the history re-reads itself while a run is still
// going. A run asked for on the page (#1363) is queued and executed by a
// worker, so the answer arrives after the request that started it; without this
// the person who pressed Run would watch a row that says "pending" until they
// reloaded the page themselves.
//
// The poll stops the moment nothing is in flight, so a page of finished runs
// costs one request.
export const RUN_POLL_MS = 3_000;

export function useScriptRuns(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", RUN_PAGE_SIZE],
    queryFn: () =>
      apiFetch<ListResponse<ScriptRun>>(`/scripts/${scriptID}/runs?per_page=${RUN_PAGE_SIZE}`),
    enabled: !!scriptID && owned,
    refetchInterval: (query) => (hasRunInFlight(query.state.data) ? RUN_POLL_MS : false),
  });
}

// hasRunInFlight reports whether any run in the history has yet to finish.
// Those two statuses are the queue's, not the outcome's: everything else is a
// run that has stopped moving.
export function hasRunInFlight(data: { data: ScriptRun[] } | undefined): boolean {
  return (data?.data ?? []).some((r) => r.status === "pending" || r.status === "running");
}

// useScriptSchedule reads an owned script's cadence in full, including the
// parameters every fire binds — which the contract deliberately does not carry,
// because it is the document every surface renders and these are the owner's
// bindings. A script with no schedule is a normal state rather than a failure,
// so the 404 the route answers with becomes null here.
export function useScriptSchedule(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "schedule"],
    queryFn: async () => {
      try {
        return await apiFetch<ScriptSchedule>(`/scripts/${scriptID}/schedule`);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    enabled: !!scriptID && owned,
  });
}

// useSetScriptSchedule creates or replaces a script's cadence. Every script
// query is invalidated on success rather than just the schedule: the contract
// reports the next fire too, and a page showing yesterday's cadence beside
// today's is worse than one extra read.
export function useSetScriptSchedule(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ScriptScheduleInput) =>
      apiFetch<ScriptSchedule>(`/scripts/${scriptID}/schedule`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

// useSetScriptSchedulePaused pauses or resumes a schedule without touching its
// cadence. It is a separate route from the cadence for a reason worth keeping
// here: sending the whole schedule back to turn it off would re-base its next
// fire, and a paused schedule must resume on the fire it was parked on.
export function useSetScriptSchedulePaused(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      apiFetch<ScriptSchedule>(`/scripts/${scriptID}/schedule/${enabled ? "enable" : "disable"}`, {
        method: "POST",
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

export function useScriptRun(scriptID: string | null, runID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", runID],
    queryFn: () => apiFetch<ScriptRunDetail>(`/scripts/${scriptID}/runs/${runID}`),
    enabled: !!scriptID && !!runID,
  });
}

// ScriptConnectionChoice is one connection a `connection` parameter may name
// (#1361): the value bound into a run, and what a person picks it by.
export interface ScriptConnectionChoice {
  name: string;
  kind?: string;
  description?: string;
}

// ScriptConnectionChoices is the set a connection parameter chooses from, and
// where it came from: the connections the caller's persona reaches, narrowed
// to the kind the parameter binds. A run is authorized at query time against
// the roles captured at the script's last save.
export interface ScriptConnectionChoices {
  data: ScriptConnectionChoice[];
  source: string;
  note: string;
}

// useScriptConnections reads the connections a parameter of this script may
// name. It is requested only for an owned script that actually declares a
// connection parameter: every other script has no use for the set, and asking
// for it would put a request on every script page.
export function useScriptConnections(scriptID: string | null, enabled: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "connections"],
    queryFn: () =>
      apiFetch<ScriptConnectionChoices>(`/scripts/${scriptID}/connections`),
    enabled: !!scriptID && enabled,
  });
}

// ScriptRunQueued identifies a run this page asked for. It carries no result:
// the run is executed by a worker, and the history below the form is where it
// is followed.
export interface ScriptRunQueued {
  run_id: string;
  status: string;
  version: number;
  message: string;
}

// useRunScript queues one run of a script's latest saved version (#1363). It
// is the same action run_script performs — the run is executed by a worker
// under the script's own identity — so this hook cannot make a script run that
// the run gate refuses.
//
// Every script query is invalidated on success, because the run belongs in the
// history immediately: a queued run that does not appear until the next poll
// reads as a button that did nothing.
export function useRunScript(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: Record<string, unknown>) =>
      apiFetch<ScriptRunQueued>(`/scripts/${scriptID}/runs`, {
        method: "POST",
        body: JSON.stringify({ params }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

// ScriptValidation is what an edited source would reach, and what is wrong with
// it (#1364). It executes nothing.
export interface ScriptValidation {
  ok: boolean;
  findings: ScriptFinding[];
  capabilities: string[];
  connections: string[];
  // Where this script's OUTPUTS go: what platform.export writes to, plus the
  // portal for an export naming none. Not every byte the script can move — a
  // write made through platform.call is read in tools instead.
  destinations: string[];
  // The tool names the source passes to platform.call literally. The persona
  // filter decides what a run may call; this is what the source does call.
  tools?: string[];
  // The output names platform.publish_data refreshes: which asset's data
  // region this source rewrites.
  refresh_targets?: string[];
  dynamic_connections: boolean;
  dynamic_destinations: boolean;
  dynamic_refresh_targets?: boolean;
  // dynamic_tools is true when a call computes the tool it invokes, which
  // shortens the tool list. A computed argument set shortens connections.
  dynamic_tools?: boolean;
  // reads_state and saves_state say what the source does with the state a
  // script carries between runs (#1537): run.state on the way in,
  // platform.save_state on the way out.
  reads_state?: boolean;
  saves_state?: boolean;
  note?: string;
}

// useValidateScriptSource parses an edit and reports what it would reach.
// Nothing is stored, so nothing is invalidated.
export function useValidateScriptSource(scriptID: string) {
  return useMutation({
    mutationFn: (source: string) =>
      apiFetch<ScriptValidation>(`/scripts/${scriptID}/validate`, {
        method: "POST",
        body: JSON.stringify({ source }),
      }),
  });
}

// ScriptDryRun is one draft execution as the editor reports it. A failed run
// answers with the same fields a successful one does: the log is the whole
// reason to have run it.
export interface ScriptDryRun {
  run_id: string;
  status: string;
  error?: string;
  log?: string;
  log_truncated?: boolean;
  metrics: { steps: number; duration_ms: number; queries: number; exports: number };
  outputs: ScriptDryRunOutput[];
  // state is the object the source would have saved with platform.save_state,
  // absent when it saved none. The draft persists it no more than an output.
  state?: Record<string, unknown>;
  message: string;
}

// useDryRunScript executes an edit as the caller, persisting nothing it
// produced. It introduces no authority: the run is the caller's own session,
// their persona and their audit trail, so it reaches exactly what they reach.
export function useDryRunScript(scriptID: string) {
  return useMutation({
    mutationFn: (input: { source: string; params: Record<string, unknown> }) =>
      apiFetch<ScriptDryRun>(`/scripts/${scriptID}/dry-run`, {
        method: "POST",
        body: JSON.stringify(input),
      }),
  });
}
