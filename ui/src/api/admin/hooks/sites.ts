import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";

// The administrators' side of Sites (#2559): every site address and domain on
// the deployment, which a system administrator adds ingress rules for, and the
// settings section.

export interface DeploymentAddress {
  hostname: string;
  kind: "site_address" | "domain" | "application";
  site?: string;
  environment?: string;
  application?: string;
  created_by: string;
  created_at: string;
}

export interface SiteSettings {
  pages_domain: string;
  max_release_files: number;
  max_file_bytes: number;
  max_release_bytes: number;
  crawl_max_pages: number;
  crawl_max_bytes: number;
  /** How many releases an environment with no limit of its own keeps (#2602); 0 keeps every release. */
  releases_keep?: number;
  /** What the daily cleanup does: "delete", or "report", which deletes nothing. */
  releases_mode?: "delete" | "report";
  updated_by?: string;
  updated_at?: string;
}

/** One cleanup (#2602): counts only. */
export interface SiteSweep {
  mode: "delete" | "report";
  run_by?: string;
  scope?: string;
  sites_swept: number;
  releases: number;
  rewritten: number;
  files: number;
  bytes: number;
  orphans: number;
  orphan_bytes: number;
  skipped: number;
  refused: number;
  failed: number;
  started_at: string;
  finished_at: string;
}

export interface SiteRetention {
  keep_default: number;
  mode: "delete" | "report";
  orphan_grace: string;
  every: string;
  sweeps: SiteSweep[];
}

const ADDRESSES = ["admin", "sites", "addresses"] as const;
const SETTINGS = ["admin", "sites", "settings"] as const;

export function useDeploymentAddresses() {
  return useQuery({
    queryKey: ADDRESSES,
    queryFn: () => apiFetch<{ addresses: DeploymentAddress[] }>("/sites/domains"),
  });
}

export function useAddDeploymentDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: { hostname: string; site?: string; environment?: string; application?: string; requires_approval?: boolean }) =>
      apiFetch("/sites/domains", { method: "POST", body: JSON.stringify(b) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ADDRESSES }),
  });
}

export function useRemoveDeploymentDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (hostname: string) => {
      const res = await apiFetchRaw(`/sites/domains/${encodeURIComponent(hostname)}`, { method: "DELETE" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new ApiError(res.status, body.detail ?? res.statusText);
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ADDRESSES }),
  });
}

export function useSiteSettings() {
  return useQuery({ queryKey: SETTINGS, queryFn: () => apiFetch<SiteSettings>("/settings/sites") });
}

export function useSaveSiteSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: SiteSettings) => apiFetch<SiteSettings>("/settings/sites", { method: "PUT", body: JSON.stringify(b) }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "sites", "retention"] });
      return qc.invalidateQueries({ queryKey: SETTINGS });
    },
  });
}

/** Release retention as it stands: the default, the mode and the recent cleanups. */
export function useSiteRetention() {
  return useQuery({
    queryKey: ["admin", "sites", "retention"],
    queryFn: () => apiFetch<SiteRetention>("/sites/retention"),
  });
}
