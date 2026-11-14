import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";
import type { ScriptEndpoint } from "./scriptEndpoint";
import type { ScriptSchedule } from "./scripts";

// A script's handlers (#2441, #2434, #2435, #2436): what starts it. A script
// may hold several of each kind: endpoints (an endpoint on a form script is a
// form), schedules, webhook sources whose events each start one run, and the
// tool-call kinds an administrator binds to a connection: a policy decides
// whether a call runs, a transform changes what it returns, a watch runs the
// script after it.

/** CallHandlerKind is a tool-call handler's kind (#2436, #2634, #2635). */
export type CallHandlerKind = "policy" | "transform" | "watch";

/** CALL_HANDLER_KINDS are the tool-call kinds, in the order a connection lists them. */
export const CALL_HANDLER_KINDS: CallHandlerKind[] = ["policy", "transform", "watch"];

/** HandlerKind is a kind of handler. */
export type HandlerKind = "endpoint" | "schedule" | "webhook" | CallHandlerKind;

/** HandlerType is what a list filters and badges by: a kind, or form. */
export type HandlerType = "endpoint" | "form" | "schedule" | "webhook" | CallHandlerKind;

/** HANDLER_TYPE_LABELS name each type as the portal shows it. */
export const HANDLER_TYPE_LABELS: Record<HandlerType, string> = {
  endpoint: "Endpoint",
  form: "Form",
  schedule: "Schedule",
  webhook: "Webhook",
  policy: "Policy",
  transform: "Transform",
  watch: "Watch",
};

/** isCallHandlerKind reports whether a kind is a tool-call handler's. */
export function isCallHandlerKind(kind: string): kind is CallHandlerKind {
  return (CALL_HANDLER_KINDS as string[]).includes(kind);
}

/** HandlerDetail is one handler as its kind describes it. */
export interface HandlerDetail extends Record<string, unknown> {
  handler: string;
  kind: HandlerKind;
  type: HandlerType;
  /** name is the script's name. */
  name: string;
  enabled: boolean;
}

/** HandlerOffer is one kind as adding a handler offers it. */
export interface HandlerOffer {
  kind: HandlerKind;
  allowed: boolean;
  reason?: string;
  contract: string;
}

/** ScriptHandlers is a script's handlers and what adding one offers. */
export interface ScriptHandlers {
  handlers: HandlerDetail[];
  add: HandlerOffer[];
  /** endpoint is what adding an endpoint offers: page state, form choices. */
  endpoint?: Partial<ScriptEndpoint>;
  /** sources are the webhook sources the caller may run the script for. */
  sources: string[];
  max_per_kind: number;
}

/** HandlerRow is one handler as Admin > Handlers lists it. */
export interface HandlerRow {
  id: string;
  kind: HandlerKind;
  type: HandlerType;
  script_id: string;
  script_name: string;
  owner_email: string;
  target: string;
  enabled: boolean;
  created_by: string;
  created_at: string;
}

/** HandlerInput is a handler added or changed: its kind and settings. */
export type HandlerInput = Record<string, unknown> & { kind?: HandlerKind };

export const handlersKey = (scriptID: string | null) => [...scriptsKey, scriptID, "handlers"];

/** useScriptHandlers reads a script's handlers, for its owner or an administrator. */
export function useScriptHandlers(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: handlersKey(scriptID),
    queryFn: () => apiFetch<ScriptHandlers>(`/scripts/${scriptID}/handlers`),
    enabled: !!scriptID && owned,
  });
}

// Every script query is invalidated after a handler write: the listing's
// badges and the contract's schedules read the same handlers.
function useInvalidateScripts() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: scriptsKey });
}

/** useSaveHandler adds a handler to a script, or changes the one handlerID names. */
export function useSaveHandler(scriptID: string, handlerID?: string) {
  const invalidate = useInvalidateScripts();
  return useMutation({
    mutationFn: (input: HandlerInput) =>
      handlerID
        ? apiFetch<HandlerDetail>(`/handlers/${handlerID}`, { method: "PUT", body: JSON.stringify(input) })
        : apiFetch<HandlerDetail>(`/scripts/${scriptID}/handlers`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: invalidate,
  });
}

/** useSetHandlerEnabled turns one handler on or off. */
export function useSetHandlerEnabled(handlerID: string) {
  const invalidate = useInvalidateScripts();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      apiFetch<HandlerDetail>(`/handlers/${handlerID}/${enabled ? "enable" : "disable"}`, { method: "POST" }),
    onSuccess: invalidate,
  });
}

/** useDeleteHandler deletes one handler. */
export function useDeleteHandler(handlerID: string) {
  const invalidate = useInvalidateScripts();
  return useMutation({
    mutationFn: () => apiFetch(`/handlers/${handlerID}`, { method: "DELETE" }),
    onSuccess: invalidate,
  });
}

/** scheduleOf is a schedule handler in the shape the schedule editor reads. */
export function scheduleOf(h: HandlerDetail): ScriptSchedule {
  return {
    id: h.handler,
    script_id: "",
    cron_spec: String(h.cron ?? ""),
    timezone: String(h.timezone ?? "UTC"),
    enabled: h.enabled,
    params: (h.args as Record<string, unknown> | undefined) ?? {},
    next_run_at: h.next_run_at as string | undefined,
    last_fire_at: h.last_fire_at as string | undefined,
    missed_fires: (h.missed_fires as number | undefined) ?? 0,
    created_by: h.created_by as string | undefined,
  };
}

/** endpointOf is an endpoint handler in the shape the endpoint editor reads. */
export function endpointOf(h: HandlerDetail): ScriptEndpoint {
  return { ...(h as unknown as ScriptEndpoint), bound: true };
}
