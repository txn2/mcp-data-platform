import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// A script's endpoint handlers (#2171, #2441): each an HTTP route at
// /endpoints/{name} that runs its handle(req) on each request, with the
// requests that failed. The owner and an administrator read them; an
// administrator adds any, changes one, or turns it on and off, and the owner
// does the same on a script that is a form (#2494). A script may answer
// several endpoints; they are read with the script's other handlers
// (scriptHandlers.ts).

// EndpointAuthMode is who an endpoint answers (#2259): a platform API key or a
// browser session among its callers, anyone with no check, or anyone who
// passes the bot check it names.
export type EndpointAuthMode = "authenticated" | "public" | "bot_check";

// ListedEndpoint is a listed script's endpoint (#2259): where it answers, who
// it admits and whether it is on.
export interface ListedEndpoint {
  name: string;
  /** path is the address, with the template's parameters in braces. */
  path: string;
  auth_mode: EndpointAuthMode;
  enabled: boolean;
}

export interface EndpointFailure {
  id: string;
  version: number;
  method: string;
  status: number;
  error: string;
  duration_ms: number;
  created_at: string;
}

export interface ScriptEndpoint {
  /** handler is the endpoint handler's id, absent for one not added yet. */
  handler?: string;
  name: string;
  bound: boolean;
  endpoint?: string;
  url?: string;
  methods?: string[];
  path?: string;
  auth_mode?: EndpointAuthMode;
  // callers is who an authenticated endpoint admits: "*", "persona:<name>"
  // or "api_key:<name>"; empty admits no one.
  callers?: string[];
  // bot_check is the check a bot_check endpoint names, and checked_methods
  // the methods it checks, empty for every one.
  bot_check?: string;
  checked_methods?: string[];
  enabled?: boolean;
  timeout_seconds?: number;
  max_memory_bytes?: number;
  max_body_bytes?: number;
  allowed_origins?: string[];
  response_mode?: "api" | "page";
  page_cdns?: string[];
  /** The sites a page may frame, call and post its forms to (#2554). */
  page_frame_origins?: string[];
  page_connect_origins?: string[];
  page_form_origins?: string[];
  /** Where the page answers; absent for a script in no application. */
  page_address?: string;
  /** Why the script cannot be a page, when it cannot. */
  page_problem?: string;
  rate_limit?: number;
  caller_rate_limit?: number;
  /** ip_rate_limit is each client address's limit a minute, 0 for none (#2556). */
  ip_rate_limit?: number;
  /** warning is what a save says beside its outcome: that the deployment
   * trusts no proxy for a client's address (#2556). */
  warning?: string;
  updated_by?: string;
  updated_at?: string;
  failures?: EndpointFailure[];
  /** is_form is whether the script defines form_spec and form_submit (#2176). */
  is_form?: boolean;
  /** form is where a form's submissions are stored, once its source is made
   * (#2178). */
  form?: FormSource;
  /** form_connections are the connections a form's source can be made on,
   * before it is. */
  form_connections?: string[];
  /** page_url is a bound form's hosted page (#2331), absolute when the
   * deployment names its public address. */
  page_url?: string;
}

// FormSource is the webhook source a form's accepted submissions are stored
// in (#2178), and the personas a script may decrypt its sensitive fields as
// (#2179).
export interface FormSource {
  source: string;
  table: string;
  connection: string;
  decrypt_personas: string[];
}

// ScriptEndpointInput is a binding set on a script; a field left out keeps
// what the binding had, or takes the default on a new one.
export interface ScriptEndpointInput {
  endpoint?: string;
  methods?: string[];
  path?: string;
  auth_mode?: EndpointAuthMode;
  callers?: string[];
  bot_check?: string;
  checked_methods?: string[];
  timeout_seconds?: number;
  max_memory_bytes?: number;
  max_body_bytes?: number;
  allowed_origins?: string[];
  /** Whether the endpoint answers with JSON or text, or serves a browser page (#2517). */
  response_mode?: "api" | "page";
  /** The https origins a page may load from besides its own. */
  page_cdns?: string[];
  page_frame_origins?: string[];
  page_connect_origins?: string[];
  page_form_origins?: string[];
  rate_limit?: number;
  caller_rate_limit?: number;
  /** ip_rate_limit is each client address's limit a minute; 0 removes it. */
  ip_rate_limit?: number;
  /** connection is where a form's source is made, named once (#2178). */
  connection?: string;
  /** decrypt_personas replaces the personas a form's sensitive fields may be
   * decrypted as (#2179); left out keeps them. */
  decrypt_personas?: string[];
}

// useSetScriptEndpoint adds an endpoint handler to the script, or changes the
// one handlerID names.
export function useSetScriptEndpoint(scriptID: string, handlerID?: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ScriptEndpointInput) =>
      handlerID
        ? apiFetch<ScriptEndpoint>(`/handlers/${handlerID}`, { method: "PUT", body: JSON.stringify(input) })
        : apiFetch<ScriptEndpoint>(`/scripts/${scriptID}/handlers`, {
            method: "POST",
            body: JSON.stringify({ kind: "endpoint", ...input }),
          }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}

// useSetScriptEndpointEnabled turns one endpoint handler on or off.
export function useSetScriptEndpointEnabled(handlerID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      apiFetch(`/handlers/${handlerID}/${enabled ? "enable" : "disable"}`, { method: "POST" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: scriptsKey }),
  });
}
