import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// How a script's event runs stand (#2332): each event a webhook source stores
// enqueues one run of every script with a webhook handler for it (#2434),
// with trigger event.

/** ScriptEventSummary is how a script's event runs stand. */
export interface ScriptEventSummary {
  pending: number;
  skipped_since: number;
  skipped_window_days: number;
  max_queued: number;
  last_event_run?: string;
  last_skipped_at?: string;
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
