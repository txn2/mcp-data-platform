import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// The webhook source a script runs for (#2332): each event it stores
// enqueues one run of the script, with trigger event.

/** ScriptEventSummary is how a script's event runs stand. */
export interface ScriptEventSummary {
  pending: number;
  skipped_since: number;
  skipped_window_days: number;
  max_queued: number;
  last_event_run?: string;
  last_skipped_at?: string;
}

/** ScriptEventSourceOutcome is the saved source and the sentence stating it. */
export interface ScriptEventSourceOutcome {
  source: string;
  message: string;
}

const eventsKey = (scriptID: string) => [...scriptsKey, scriptID, "events"] as const;

/** useScriptEventSummary reads how a script's event runs stand. */
export function useScriptEventSummary(scriptID: string, enabled = true) {
  return useQuery({
    queryKey: eventsKey(scriptID),
    queryFn: () => apiFetch<ScriptEventSummary>(`/scripts/${scriptID}/events`),
    enabled,
    refetchInterval: 30_000,
  });
}

/** useSetScriptEventSource sets the webhook source a script runs for; "" runs it for none. */
export function useSetScriptEventSource(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (source: string) =>
      apiFetch<ScriptEventSourceOutcome>(`/scripts/${scriptID}/event-source`, {
        method: "PUT",
        body: JSON.stringify({ source }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}
