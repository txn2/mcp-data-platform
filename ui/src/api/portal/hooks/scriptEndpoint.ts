import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// A script's endpoint (#2171): the HTTP route at /endpoints/{name} that runs
// its handle(req) on each request, and the requests that failed. The owner
// and an administrator read it; only an administrator binds a script, changes
// its binding, or turns it on and off.

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
  rate_limit?: number;
  caller_rate_limit?: number;
  updated_by?: string;
  updated_at?: string;
  failures?: EndpointFailure[];
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
  rate_limit?: number;
  caller_rate_limit?: number;
}

const endpointKey = (scriptID: string | null) => [...scriptsKey, scriptID, "endpoint"];

// useScriptEndpoint reads a script's binding. A deployment that cannot bind
// scripts answers 404, which reads here as null rather than as a failure.
export function useScriptEndpoint(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: endpointKey(scriptID),
    queryFn: async () => {
      try {
        return await apiFetch<ScriptEndpoint>(`/scripts/${scriptID}/endpoint`);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    enabled: !!scriptID && owned,
  });
}

// useSetScriptEndpoint binds the script, or changes its binding.
export function useSetScriptEndpoint(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ScriptEndpointInput) =>
      apiFetch<ScriptEndpoint>(`/scripts/${scriptID}/endpoint`, {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: endpointKey(scriptID) }),
  });
}

// useSetScriptEndpointEnabled turns the endpoint on or off.
export function useSetScriptEndpointEnabled(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (enabled: boolean) =>
      apiFetch(`/scripts/${scriptID}/endpoint/${enabled ? "enable" : "disable"}`, { method: "POST" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: endpointKey(scriptID) }),
  });
}
