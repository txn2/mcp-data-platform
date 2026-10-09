import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type {
  MapEstimate,
  MapRegion,
  MapRegionInput,
  MapSettingsInput,
  MapsView,
} from "../types";

// Street maps settings (#2068).

const KEY = ["settings", "maps"] as const;

// A fetch moves through queued and fetching on its own; while one is in
// flight the section is read again every two seconds so its progress moves.
const IN_FLIGHT_POLL_MS = 2000;

export function useMaps() {
  return useQuery({
    queryKey: KEY,
    queryFn: () => apiFetch<MapsView>("/settings/maps"),
    refetchInterval: (q) =>
      q.state.data?.regions.some((r) => r.state === "queued" || r.state === "fetching")
        ? IN_FLIGHT_POLL_MS
        : false,
  });
}

export function useSetMapSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: MapSettingsInput) =>
      apiFetch<MapsView>("/settings/maps", { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => {
      qc.setQueryData(KEY, data);
    },
  });
}

export function useAddMapRegion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: MapRegionInput) =>
      apiFetch<MapRegion>("/settings/maps/regions", { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}

export function useRefreshMapRegion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<MapRegion>(`/settings/maps/regions/${encodeURIComponent(id)}/refresh`, {
        method: "POST",
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}

export function useDeleteMapRegion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await apiFetchRaw(`/settings/maps/regions/${encodeURIComponent(id)}`, {
        method: "DELETE",
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new Error(body.detail ?? `Deleting the region failed with status ${res.status}`);
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}

/** useEstimateMapRegion asks what a region would cost, writing nothing. */
export function useEstimateMapRegion() {
  return useMutation({
    mutationFn: (input: Pick<MapRegionInput, "preset" | "bounds">) =>
      apiFetch<MapEstimate>("/settings/maps/estimate", { method: "POST", body: JSON.stringify(input) }),
  });
}
