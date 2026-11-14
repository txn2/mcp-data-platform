import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";
import type { CallHandlerKind, HandlerDetail } from "./scriptHandlers";

// Tool-call handlers on a connection (#2436, #2634, #2635): the policies that
// decide whether a call runs, the transforms that change what it returns, in
// order, and the watches that run a script after it. Every route here is an
// administrator's; the server refuses anyone else.

/** CallCondition limits a handler to calls whose argument equals a value. */
export interface CallCondition {
  in: "path" | "body" | "query";
  name: string;
  equals: string;
}

/** HandlerFailure is a policy or transform run that failed, kept 7 days. */
export interface HandlerFailure {
  id: string;
  handler_id: string;
  version: number;
  user: string;
  call: Record<string, unknown>;
  prints: string;
  error: string;
  outcome: string;
  duration_ms: number;
  at: string;
}

/** HandlerStats is a handler's last day on a connection's calls. */
export interface HandlerStats {
  matched: number;
  denied: number;
  held: number;
  errors: number;
  queued: number;
  p50_ms: number;
  p99_ms: number;
}

/** CallHandlerDetail is a tool-call handler as the platform describes it. */
export interface CallHandlerDetail extends HandlerDetail {
  kind: CallHandlerKind;
  script_id: string;
  connection: string;
  operations?: string[] | null;
  personas?: string[] | null;
  users?: string[] | null;
  conditions: CallCondition[];
  args?: Record<string, unknown> | null;
  timeout_ms: number;
  on_failure: string;
  items_path: string;
  writes?: Record<string, boolean> | null;
  protected?: string[] | null;
  order: number;
  contract: string;
  created_by?: string;
  running_version?: number;
  approved_by?: string;
  waiting_version?: number;
  recent_failures?: HandlerFailure[];
  stats?: HandlerStats;
}

/** TestStep is one handler a tested call reached, and what came of it. */
export interface TestStep {
  id: string;
  kind: string;
  script: string;
  outcome: string;
  ms: number;
  says?: string;
}

/** TestOutcome is what the handlers on a connection make of a call. */
export interface TestOutcome {
  outcome: "runs" | "denied" | "held";
  handlers: TestStep[];
  not_applying: { id: string; kind: string; script: string; operations?: string[] | null }[];
  reason?: string;
  denied_by?: string;
  approvers?: string[];
  message?: string;
  transforms?: number;
  watches?: number;
  result?: unknown;
  transform_error?: string;
}

/** TestCall is a call the test panel asks about. */
export interface TestCall {
  tool: string;
  arguments?: Record<string, unknown>;
  as_user?: string;
  as_persona?: string;
  at?: string;
  result?: unknown;
}

const connectionHandlersKey = (connection: string) => [...scriptsKey, "connection-handlers", connection];

/** useConnectionHandlers reads every tool-call handler on a connection. */
export function useConnectionHandlers(connection: string, enabled = true) {
  return useQuery({
    queryKey: connectionHandlersKey(connection),
    queryFn: () =>
      apiFetch<{ data: CallHandlerDetail[] }>(`/connections/${encodeURIComponent(connection)}/handlers`).then(
        (r) => r.data,
      ),
    enabled: enabled && connection !== "",
  });
}

/** useTestCall asks what the handlers on a connection make of a call. */
export function useTestCall(connection: string) {
  return useMutation({
    mutationFn: (call: TestCall) =>
      apiFetch<TestOutcome>(`/connections/${encodeURIComponent(connection)}/handlers/test`, {
        method: "POST",
        body: JSON.stringify(call),
      }),
  });
}

/** useOrderTransforms sets the order a connection's transforms run in. */
export function useOrderTransforms(connection: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (ids: string[]) =>
      apiFetch<{ data: CallHandlerDetail[] }>(`/connections/${encodeURIComponent(connection)}/handlers/order`, {
        method: "PUT",
        body: JSON.stringify({ ids }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

/** useHandlerFailures reads a tool-call handler's newest failures. */
export function useHandlerFailures(handlerID: string, enabled: boolean) {
  return useQuery({
    queryKey: [...scriptsKey, "handler-failures", handlerID],
    queryFn: () => apiFetch<{ data: HandlerFailure[] }>(`/handlers/${handlerID}/failures`).then((r) => r.data),
    enabled,
  });
}
