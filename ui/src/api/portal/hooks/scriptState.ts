import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// The state a script carries between runs (#1537): one JSON object the
// platform keeps per script, read by a run as run.state at creation and saved
// by platform.save_state when the run succeeds. These hooks are the owner's
// and an administrator's read of it and their two resets.

// ScriptState is the object as the portal reports it.
export interface ScriptState {
  state: Record<string, unknown>;
  // revision counts writes; 0 means nothing was ever saved or reset.
  revision: number;
  updated_at?: string;
  // run_id names the run that wrote this revision, updated_by the person who
  // set or cleared it; one of the two is set past revision 0.
  run_id?: string;
  updated_by?: string;
  message?: string;
}

// ScriptContractState is the contract's account of a script's state: what the
// source does with it, read statically, and the revision the platform holds.
export interface ScriptContractState {
  reads_state: boolean;
  saves_state: boolean;
  revision: number;
  updated_at?: string;
}

// useScriptState reads the state a script carries between runs. It is owner
// and admin reading, like the runs it explains, so it is requested only for an
// owned script. A deployment that keeps no state answers 404, which reads here
// as null rather than as a failure: the card then says so instead of failing.
export function useScriptState(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "state"],
    queryFn: async () => {
      try {
        return await apiFetch<ScriptState>(`/scripts/${scriptID}/state`);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    enabled: !!scriptID && owned,
  });
}

// useSetScriptState replaces the whole state object. Every script query is
// invalidated on success: the contract carries the state's revision, and a
// run history read beside a state it no longer matches is worse than one
// extra read.
export function useSetScriptState(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (state: Record<string, unknown>) =>
      apiFetch<ScriptState>(`/scripts/${scriptID}/state`, {
        method: "PUT",
        body: JSON.stringify({ state }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

// useClearScriptState resets the state to {} so the next run starts over. It
// is the recovery for a wrong watermark, and it moves the revision: a run in
// flight that read the old one fails at its write.
export function useClearScriptState(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<ScriptState>(`/scripts/${scriptID}/state`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}
