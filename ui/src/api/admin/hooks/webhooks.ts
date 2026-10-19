import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type {
  WebhookAlertInput,
  WebhookAlertSettings,
  WebhookCustomSpan,
  WebhookSource,
  WebhookSourceDetail,
  WebhookSourceInput,
  WebhookStatusOverview,
  WebhookStatusRange,
} from "../types";
import { REFETCH_INTERVAL } from "./shared";

// Inbound webhook sources (#1870).

const KEY = ["webhook-sources"] as const;

/** useWebhookStatus reads the status of every source in one request (#1979),
 * or of one source when `source` names it (#2107), refreshed so a source that
 * goes quiet or starts failing shows without a reload. `enabled` false holds
 * the request. */
export function useWebhookStatus(range: WebhookStatusRange, source = "", enabled = true, custom?: WebhookCustomSpan) {
  const params = new URLSearchParams({ range });
  if (source) params.set("source", source);
  if (range === "custom" && custom) {
    params.set("from", custom.from);
    params.set("to", custom.to);
  }
  return useQuery({
    queryKey: [...KEY, "status", range, source, range === "custom" ? custom : null],
    queryFn: () => apiFetch<WebhookStatusOverview>(`/webhooks/status?${params.toString()}`),
    enabled: enabled && (range !== "custom" || !!custom),
    refetchInterval: REFETCH_INTERVAL,
    // Switching the range keeps the table on screen while the series loads.
    placeholderData: keepPreviousData,
  });
}

/** useWebhookSources lists every source, for a picker naming one. */
export function useWebhookSources() {
  return useQuery({
    queryKey: [...KEY, "list"],
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

/** useSyncWebhookPartitions lists every raw file a source holds and makes its
 * raw partitions match (#2133): the repair, never part of retention. */
export function useSyncWebhookPartitions() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (name: string) => {
      const res = await apiFetchRaw(`/webhooks/sources/${encodeURIComponent(name)}/sync-partitions`, {
        method: "POST",
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new Error(body.detail ?? `Resyncing the partitions failed with status ${res.status}`);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}

// --- Webhook alerts (#2149) ---

const ALERT_KEY = ["settings", "webhook-alert"] as const;

export function useWebhookAlertSettings() {
  return useQuery({
    queryKey: ALERT_KEY,
    queryFn: () => apiFetch<WebhookAlertSettings>("/settings/webhook-alert"),
  });
}

export function useSetWebhookAlertSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: WebhookAlertInput) =>
      apiFetch<WebhookAlertSettings>("/settings/webhook-alert", { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => {
      qc.setQueryData(ALERT_KEY, data);
    },
  });
}
