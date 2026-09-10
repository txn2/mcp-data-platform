// The two checks an author runs against an edit before saving it (#1364):
// validate, which parses and reports what the source would reach, and the dry
// run, which executes it as the author.
//
// They live apart from the rest of the script hooks because they are the only
// pair that acts on text the server has not seen: everything else in scripts.ts
// addresses a saved record.
import { useMutation } from "@tanstack/react-query";
import type { ScriptDryRunOutput, ScriptFinding } from "@/api/admin/types";
import { apiFetch } from "../client";

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
  metrics: {
    steps: number;
    duration_ms: number;
    queries: number;
    exports: number;
  };
  outputs: ScriptDryRunOutput[];
  // state is the object the source would have saved with platform.save_state,
  // absent when it saved none. The draft persists it no more than an output.
  state?: Record<string, unknown>;
  // writes are the platform.call calls that persisted for real, empty unless
  // the run was asked to write. Those calls landed, and this is the only place
  // the response says so.
  writes: ScriptDryRunWrite[];
  // refused_write is the call the write barrier stopped, absent when it stopped
  // none. At most one: the refusal ends the run.
  refused_write?: ScriptDryRunWrite;
  message: string;
}

// ScriptDryRunWrite is one persisting platform.call: the tool, and the action
// or method that made it a write.
export interface ScriptDryRunWrite {
  tool: string;
  call: string;
}

// useDryRunScript executes an edit as the caller. It introduces no authority:
// the run is the caller's own session, their persona and their audit trail, so
// it reaches exactly what they reach.
//
// It persists nothing unless allowWrites is set: platform.export reports the
// shape of each output instead of writing it, and a platform.call that would
// persist is refused and named in refused_write. With allowWrites the run
// writes for real and reports every write it made.
export function useDryRunScript(scriptID: string) {
  return useMutation({
    mutationFn: (input: {
      source: string;
      params: Record<string, unknown>;
      allow_writes?: boolean;
    }) =>
      apiFetch<ScriptDryRun>(`/scripts/${scriptID}/dry-run`, {
        method: "POST",
        body: JSON.stringify(input),
      }),
  });
}
