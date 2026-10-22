import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { AccessRequestSettings } from "../types";

// Requests for access (#2090).

const SETTINGS = ["settings", "access-requests"] as const;

export function useAccessRequestSettings() {
  return useQuery({
    queryKey: SETTINGS,
    queryFn: () => apiFetch<AccessRequestSettings>("/settings/access-requests"),
  });
}

export function useSetAccessRequestSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AccessRequestSettings) =>
      apiFetch<AccessRequestSettings>("/settings/access-requests", {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    onSuccess: (data) => qc.setQueryData(SETTINGS, data),
  });
}
