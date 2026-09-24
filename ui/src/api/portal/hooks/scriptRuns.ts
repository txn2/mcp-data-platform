import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";
import type { ListResponse, ScriptRun, ScriptRunDetail } from "./scripts";

// A script's runs: its history, the runs that have not ended, one run in
// full, and stopping one (#1290, #1847, #1860). They live apart from the
// script hooks for the file's size, and are re-exported there so every page
// imports script hooks from one place.

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

// useScriptLiveRuns reads the runs of a script that have not ended (#1860):
// queued, executing, or held by a worker that stopped reporting. It is read
// apart from the history because a run whose worker died can be older than
// the page of history above it, and it is the one run an owner must see.
export function useScriptLiveRuns(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", "live"],
    queryFn: () =>
      apiFetch<ListResponse<ScriptRun>>(`/scripts/${scriptID}/runs?live=true&per_page=${LIVE_RUNS}`),
    enabled: !!scriptID && owned,
    refetchInterval: (query) => (hasRunInFlight(query.state.data) ? RUN_POLL_MS : false),
  });
}

// LIVE_RUNS caps the runs the live panel lists.
export const LIVE_RUNS = 20;

export function useScriptRuns(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", RUN_PAGE_SIZE],
    queryFn: () =>
      apiFetch<ListResponse<ScriptRun>>(
        `/scripts/${scriptID}/runs?per_page=${RUN_PAGE_SIZE}`,
      ),
    enabled: !!scriptID && owned,
    refetchInterval: (query) =>
      hasRunInFlight(query.state.data) ? RUN_POLL_MS : false,
  });
}

// hasRunInFlight reports whether any run in the history has yet to finish.
// Those two statuses are the queue's, not the outcome's: everything else is a
// run that has stopped moving.
export function hasRunInFlight(
  data: { data: ScriptRun[] } | undefined,
): boolean {
  return (data?.data ?? []).some(isRunInFlight);
}

// useScriptRun reads one run in full. While the run is still queued or
// executing it is re-read on the history's interval, so an open run shows its
// progress and the log so far as the worker writes them (#1847) rather than
// the state it was in when it was opened.
export function useScriptRun(scriptID: string | null, runID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "runs", runID],
    queryFn: () =>
      apiFetch<ScriptRunDetail>(`/scripts/${scriptID}/runs/${runID}`),
    enabled: !!scriptID && !!runID,
    refetchInterval: (query) =>
      isRunInFlight(query.state.data) ? RUN_POLL_MS : false,
  });
}

// isRunInFlight reports whether one run has yet to finish.
export function isRunInFlight(run: { status: string } | undefined): boolean {
  return run?.status === "pending" || run?.status === "running";
}

// ScriptRunCancelled is what a cancel did: canceled (it had not started and
// will not), requested (it is running and ends canceled within seconds),
// canceled_orphaned (its worker had stopped reporting, so it was ended at once,
// #1860) or already_finished.
export interface ScriptRunCancelled {
  run_id: string;
  status: string;
  outcome: "canceled" | "requested" | "canceled_orphaned" | "already_finished";
  message: string;
}

// useCancelScriptRun stops a run (#1847). Every script query is invalidated,
// because the run's status, the history row and the listing all change.
export function useCancelScriptRun(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (runID: string) =>
      apiFetch<ScriptRunCancelled>(`/scripts/${scriptID}/runs/${runID}/cancel`, {
        method: "POST",
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

