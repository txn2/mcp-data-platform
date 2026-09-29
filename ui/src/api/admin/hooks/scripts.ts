import { useQuery } from "@tanstack/react-query";
import type { ScriptVersion, VersionDetail } from "../types/scripts";
import { apiFetch } from "../client";

// Managed-script admin hooks: one script's version history, and one version's
// detail payload.
//
// The listing and the run listing are read from the portal routes instead
// (#1407): those answer an administrator with every script and every run, and
// one listing per question is what keeps the administrator's page and the
// owner's page from drifting apart. The admin REST routes they used to read
// are still served for API callers.

// scriptsKey is the root of every script query key.
const scriptsKey = ["admin", "scripts"] as const;

interface ListResponse<T> {
  data: T[];
  total: number;
}

export function useScriptVersions(scriptID: string | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "versions"],
    queryFn: () =>
      apiFetch<ListResponse<ScriptVersion>>(`/scripts/${scriptID}/versions`),
    enabled: !!scriptID,
  });
}

export function useScriptVersionDetail(scriptID: string | null, version: number | null) {
  return useQuery({
    queryKey: [...scriptsKey, scriptID, "versions", version],
    queryFn: () =>
      apiFetch<VersionDetail>(`/scripts/${scriptID}/versions/${version}`),
    enabled: !!scriptID && !!version,
  });
}
