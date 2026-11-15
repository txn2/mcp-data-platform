import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { useApprovals, type ApprovalRequest } from "./approvals";
import type { Site, SiteEnvironment } from "./sites";
import { apiFetch as adminFetch } from "../../admin/client";

// The newer Sites operations (#2636, #2647, #2629): the server's check of a
// new site's name, each site's traffic and each replica's, and where the
// viewer reads one site file's bytes. Re-exported by ./sites.

const root = "/sites";
const one = (site: string) => `${root}/${encodeURIComponent(site)}`;
const json = (method: string, body: unknown) => ({ method, body: JSON.stringify(body) });

/** What a typed site name becomes, and why it would be refused (#2636): the server holds the rule. */
export interface SiteNameCheck {
  input: string;
  name: string;
  addresses: Record<string, string>;
  environment_name_room: number;
  refusal?: string;
}

export function useSiteNameCheck(input: string) {
  return useQuery({
    queryKey: ["sites", "check-name", input],
    queryFn: () => apiFetch<SiteNameCheck>(`${root}/check-name`, json("POST", { name: input })),
    enabled: input.trim() !== "",
    staleTime: 30_000,
  });
}

/** One set of traffic figures (#2647). */
export interface TrafficTotals {
  requests: number;
  classes: Record<"2xx" | "3xx" | "4xx" | "5xx" | "other", number>;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
}

export type TrafficRange = "1h" | "24h" | "7d" | "30d";

export interface SiteTraffic {
  range: TrafficRange;
  step_seconds: number;
  recording: boolean;
  environment?: string;
  total: TrafficTotals;
  series: (TrafficTotals & { at: string })[];
  environments: Record<string, TrafficTotals>;
}

export function useSiteTraffic(site: string, range: TrafficRange, environment: string) {
  const q = new URLSearchParams({ range });
  if (environment) q.set("environment", environment);
  return useQuery({
    queryKey: ["sites", site, "traffic", range, environment],
    queryFn: () => apiFetch<SiteTraffic>(`${one(site)}/traffic?${q.toString()}`),
    refetchInterval: 60_000,
  });
}

export interface ReplicaTraffic extends TrafficTotals {
  instance: string;
}

/** Each replica's figures across every site, for administrators. */
export function useReplicaTraffic(range: TrafficRange, enabled: boolean) {
  return useQuery({
    queryKey: ["sites", "replicas", range],
    queryFn: () => adminFetch<{ range: TrafficRange; recording: boolean; replicas: ReplicaTraffic[] }>(`/sites/traffic/replicas?range=${range}`),
    enabled,
    refetchInterval: 60_000,
  });
}

/** Where the viewer reads one site file's bytes (#2629): dev's, or a release's. */
export function siteFileContentUrl(site: string, path: string, release?: number): string {
  const q = new URLSearchParams({ path });
  if (release) q.set("release", String(release));
  return `/api/v1/portal/sites/${encodeURIComponent(site)}/file/content?${q.toString()}`;
}

export type EnvironmentStatus = "online" | "offline" | "redirect";

/** The status an environment is in; one with none reads as online. */
export function statusOf(env: Pick<SiteEnvironment, "status">): EnvironmentStatus {
  return env.status ?? "online";
}

/** Removes an environment between dev and prod (#2630). */
export function useRemoveSiteEnvironment(site: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => apiFetch<Site>(`${one(site)}/environments/${encodeURIComponent(name)}`, { method: "DELETE" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["approvals"] });
      return qc.invalidateQueries({ queryKey: ["sites"] });
    },
  });
}

/** The newest approval per target and purpose (a promote names an environment, making public a domain), until carried out. */
export function useApprovalForSite(site: Site): (ApprovalRequest & { target: string })[] {
  const { data } = useApprovals();
  const newest = new Map<string, ApprovalRequest & { target: string }>();
  for (const r of data ?? []) {
    if ((r.subject_kind !== "site_release" && r.subject_kind !== "site_environment") || r.site_name !== site.name || !r.target) continue;
    const key = `${r.purpose}:${r.target}`;
    const held = newest.get(key);
    if (!held || r.created_at > held.created_at) newest.set(key, r as ApprovalRequest & { target: string });
  }
  return [...newest.values()].filter((r) => {
    const env = site.environments.find((e) => e.name === r.target);
    if (r.status === "pending" || r.status === "denied") return true;
    if (r.status !== "approved") return false;
    if (r.purpose === "site_public") return site.domains.some((d) => d.hostname === r.target && !d.public);
    if (r.purpose === "site_redirect") return env !== undefined && statusOf(env) !== "redirect";
    return env?.release !== r.version;
  });
}
