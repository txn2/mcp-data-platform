import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { StorageHistory, StorageReport, StorageSettings, StorageSettingsInput } from "../types";
import { REFETCH_INTERVAL } from "./shared";

// Storage capacity (#2148).

const KEY = ["storage"] as const;
const SETTINGS_KEY = ["settings", "storage"] as const;

/** useStorageReport reads every store now, refreshed so a store filling shows without a reload. */
export function useStorageReport() {
  return useQuery({
    queryKey: [...KEY, "report"],
    queryFn: () => apiFetch<StorageReport>("/storage"),
    refetchInterval: REFETCH_INTERVAL,
  });
}

/** useStorageHistory reads one store's samples over the last `days`. */
export function useStorageHistory(store: string, days: number) {
  const params = new URLSearchParams({ store, days: String(days) });
  return useQuery({
    queryKey: [...KEY, "history", store, days],
    queryFn: () => apiFetch<StorageHistory>(`/storage/history?${params.toString()}`),
    enabled: !!store,
    placeholderData: keepPreviousData,
  });
}

export function useStorageSettings() {
  return useQuery({
    queryKey: SETTINGS_KEY,
    queryFn: () => apiFetch<StorageSettings>("/settings/storage"),
  });
}

export function useSetStorageSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: StorageSettingsInput) =>
      apiFetch<StorageSettings>("/settings/storage", { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => {
      qc.setQueryData(SETTINGS_KEY, data);
      // A budget or a limit changes what the stores are judged against.
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}
