import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type { WebhookSource, WebhookSourceDetail, WebhookSourceInput } from "../types";
import { REFETCH_INTERVAL } from "./shared";

// Inbound webhook sources (#1870).

const KEY = ["webhook-sources"] as const;

export function useWebhookSources() {
  return useQuery({
    queryKey: KEY,
    queryFn: () => apiFetch<{ sources: WebhookSource[] }>("/webhooks/sources"),
  });
}

/** useWebhookSource reads one source and its status, refreshed so a quiet
 * source and a rising rejection count show without a reload. */
export function useWebhookSource(name: string) {
  return useQuery({
    queryKey: [...KEY, name],
    queryFn: () => apiFetch<WebhookSourceDetail>(`/webhooks/sources/${encodeURIComponent(name)}`),
    enabled: !!name,
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useCreateWebhookSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: WebhookSourceInput) =>
      apiFetch<WebhookSource>("/webhooks/sources", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}

export function useUpdateWebhookSource(name: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: WebhookSourceInput) =>
      apiFetch<WebhookSource>(`/webhooks/sources/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}

export function useDeleteWebhookSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (name: string) => {
      const res = await apiFetchRaw(`/webhooks/sources/${encodeURIComponent(name)}`, { method: "DELETE" });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new Error(body.detail ?? `Deleting the source failed with status ${res.status}`);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
      void qc.invalidateQueries({ queryKey: ["scratch-tables"] });
    },
  });
}
