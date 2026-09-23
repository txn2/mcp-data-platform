import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// Run grants on a script (#1846): who other than the owner may run it over
// HTTP and read the runs they started. A grantee never reads the source or
// changes the script, and the run executes as the script with its author's
// roles. These hooks are the owner's and an administrator's.

export type ScriptGrantKind = "persona" | "role" | "api_key";

export interface ScriptGrant {
  script_id: string;
  principal_kind: ScriptGrantKind;
  principal: string;
  granted_by: string;
  created_at: string;
}

interface GrantList {
  data: ScriptGrant[];
  total: number;
}

const grantsKey = (scriptID: string | null) => [...scriptsKey, scriptID, "grants"];

// useScriptGrants reads a script's grants. A deployment that keeps none
// answers 404, which reads here as null rather than as a failure.
export function useScriptGrants(scriptID: string | null, owned: boolean) {
  return useQuery({
    queryKey: grantsKey(scriptID),
    queryFn: async () => {
      try {
        return (await apiFetch<GrantList>(`/scripts/${scriptID}/grants`)).data;
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    enabled: !!scriptID && owned,
  });
}

// useAddScriptGrant grants a principal the right to run the script.
export function useAddScriptGrant(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (grant: { principal_kind: ScriptGrantKind; principal: string }) =>
      apiFetch<GrantList>(`/scripts/${scriptID}/grants`, {
        method: "POST",
        body: JSON.stringify(grant),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: grantsKey(scriptID) }),
  });
}

// useRemoveScriptGrant withdraws one grant.
export function useRemoveScriptGrant(scriptID: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (grant: { principal_kind: ScriptGrantKind; principal: string }) =>
      apiFetch<GrantList>(
        `/scripts/${scriptID}/grants/${grant.principal_kind}/${encodeURIComponent(grant.principal)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: grantsKey(scriptID) }),
  });
}
