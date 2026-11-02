import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { EmbedHostsSettings } from "../types";

// Embed hosts (#2069).

const SETTINGS = ["settings", "embed-hosts"] as const;

export function useEmbedHostsSettings() {
  return useQuery({
    queryKey: SETTINGS,
    queryFn: () => apiFetch<EmbedHostsSettings>("/settings/embed-hosts"),
  });
}

export function useSetEmbedHosts() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (hosts: string[]) =>
      apiFetch<EmbedHostsSettings>("/settings/embed-hosts", {
        method: "PUT",
        body: JSON.stringify({ hosts }),
      }),
    onSuccess: (data) => qc.setQueryData(SETTINGS, data),
  });
}
