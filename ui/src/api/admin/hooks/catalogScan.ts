import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { CatalogDocument, CatalogScan, CatalogSettings } from "../types";
import { REFETCH_INTERVAL } from "./shared";

// The platform catalog's scan settings and each connection's last scan (#1918).

const SETTINGS_KEY = ["catalog", "settings"] as const;
const SCANS_KEY = ["catalog", "scans"] as const;

export function useCatalogSettings() {
  return useQuery({
    queryKey: SETTINGS_KEY,
    queryFn: () => apiFetch<CatalogSettings>("/catalog/settings"),
  });
}

export function useSaveCatalogSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Pick<CatalogSettings, "scan_interval" | "exclusions">) =>
      apiFetch<CatalogSettings>("/catalog/settings", { method: "PUT", body: JSON.stringify(body) }),
    onSuccess: (saved) => {
      qc.setQueryData(SETTINGS_KEY, saved);
    },
  });
}

/** useCatalogScans reads each connection's last scan, refreshed so a scan
 * that was just requested shows when it finishes. */
export function useCatalogScans() {
  return useQuery({
    queryKey: SCANS_KEY,
    queryFn: () => apiFetch<{ scans: CatalogScan[] }>("/catalog/scans"),
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useRequestCatalogScan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<{ status: string }>("/catalog/scans", { method: "POST" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: SCANS_KEY });
    },
  });
}

/** useCatalogDocuments lists the schema documents the scan keeps (#1918). */
export function useCatalogDocuments() {
  return useQuery({
    queryKey: ["catalog", "documents"] as const,
    queryFn: () => apiFetch<{ documents: CatalogDocument[] }>("/catalog/documents"),
  });
}

/** catalogDocumentHref is where a schema document downloads from. */
export function catalogDocumentHref(d: Pick<CatalogDocument, "connection_kind" | "connection" | "filename">): string {
  return `/api/v1/admin/catalog/documents/${[d.connection_kind, d.connection, d.filename].map(encodeURIComponent).join("/")}`;
}
