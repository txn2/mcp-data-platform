import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";
import { useApprovals, type ApprovalRequest } from "./approvals";

// Sites (#2559): a site has one address of its own, serving its default
// environment to people who sign in and have access, and any number of domains
// serving its other environments to anyone. The server decides who may do what;
// each site reports the caller's permissions so the page hides what they would
// be refused.

export const SITE_NAME_RE = /^[a-z][a-z0-9-]{0,62}$/;
export const DEFAULT_ENVIRONMENT = "default";

export interface SiteEnvironment {
  name: string;
  release: number;
  requires_approval: boolean;
  updated_by?: string;
  updated_at: string;
}

export interface SiteDomain {
  hostname: string;
  site_id?: string;
  site?: string;
  environment?: string;
  application?: string;
  created_by: string;
  created_at: string;
}

export interface SitePermissions {
  manage: boolean;
  own: boolean;
  delete: boolean;
}

export interface Site {
  id: string;
  name: string;
  description: string;
  owner_email: string;
  internal_view: boolean;
  environments: SiteEnvironment[];
  created_by: string;
  created_at: string;
  updated_at: string;
  address: string;
  domains: SiteDomain[];
  permissions: SitePermissions;
}

export interface SiteLimits {
  max_release_files: number;
  max_file_bytes: number;
  max_release_bytes: number;
  crawl_max_pages: number;
  crawl_max_bytes: number;
}

export interface SiteList {
  sites: Site[];
  total: number;
  pages_domain: string;
  limits: SiteLimits;
  can_create: boolean;
  admin: boolean;
}

export interface SiteRedirect {
  from: string;
  to: string;
  status: number;
}

export interface SiteRelease {
  id: string;
  number: number;
  source: string;
  note: string;
  file_count: number;
  total_bytes: number;
  redirects: SiteRedirect[];
  created_by: string;
  created_at: string;
  served_in: string[];
}

export interface SiteFile {
  path: string;
  content_type: string;
  size: number;
  gzip_size?: number;
  brotli_size?: number;
}

export type SiteReleaseDetail = SiteRelease & { files: SiteFile[] };

export interface SiteComparison {
  from: number;
  to: number;
  changed: string[];
  added: string[];
  removed: string[];
  redirects_added: SiteRedirect[];
  redirects_removed: SiteRedirect[];
}

export interface SiteDeployment {
  environment: string;
  action: string;
  from: number;
  to: number;
  actor: string;
  created_at: string;
}

export interface SiteSkipped {
  address: string;
  reason: string;
}

export type SiteJobStatus = "running" | "done" | "failed" | "stopped";

export interface SiteJob {
  id: string;
  kind: "publish" | "crawl";
  status: SiteJobStatus;
  phase: string;
  environment: string;
  source: string;
  done: number;
  total: number;
  pages: number;
  bytes: number;
  skipped_count: number;
  redirects: number;
  skipped: SiteSkipped[];
  release: number;
  error?: string;
  created_by: string;
  created_at: string;
}

export interface SiteGrant {
  email: string;
  level: "manage" | "view";
  granted_by: string;
  created_at: string;
}

export interface SiteAccessList {
  owner_email: string;
  internal_view: boolean;
  grants: SiteGrant[];
}

export interface SitePromoted {
  environment: string;
  release: number;
  address?: string;
  status: string;
  approval?: { id: string };
}

const root = "/sites";
const one = (site: string) => `${root}/${encodeURIComponent(site)}`;
const keys = { all: ["sites"] as const, site: (s: string) => ["sites", s] as const };

function useInvalidate(site?: string) {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: site ? keys.site(site) : keys.all });
}

const json = (method: string, body: unknown) => ({ method, body: JSON.stringify(body) });

export function useSites() {
  return useQuery({ queryKey: [...keys.all, "list"], queryFn: () => apiFetch<SiteList>(root) });
}

export function useSite(site: string) {
  return useQuery({ queryKey: [...keys.site(site), "detail"], queryFn: () => apiFetch<Site>(one(site)) });
}

export function useSiteReleases(site: string) {
  return useQuery({
    queryKey: [...keys.site(site), "releases"],
    queryFn: () => apiFetch<{ releases: SiteRelease[] }>(`${one(site)}/releases`),
  });
}

export function useSiteRelease(site: string, number: number) {
  return useQuery({
    queryKey: [...keys.site(site), "release", number],
    queryFn: () => apiFetch<SiteReleaseDetail>(`${one(site)}/releases/${number}`),
  });
}

export function useSiteCompare(site: string, from: number, to: number) {
  return useQuery({
    queryKey: [...keys.site(site), "compare", from, to],
    enabled: to > 0,
    queryFn: () => apiFetch<SiteComparison>(`${one(site)}/compare?from=${from}&to=${to}`),
  });
}

export function useSiteDeployments(site: string, environment: string) {
  const q = environment ? `?environment=${encodeURIComponent(environment)}` : "";
  return useQuery({
    queryKey: [...keys.site(site), "deployments", environment],
    queryFn: () => apiFetch<{ deployments: SiteDeployment[] }>(`${one(site)}/deployments${q}`),
  });
}

export function useSiteAccess(site: string, enabled: boolean) {
  return useQuery({
    queryKey: [...keys.site(site), "access"],
    enabled,
    queryFn: () => apiFetch<SiteAccessList>(`${one(site)}/access`),
  });
}

/** Follows a job, asking again while it runs. */
export function useSiteJob(site: string, id: string | null) {
  const qc = useQueryClient();
  const job = useQuery({
    queryKey: [...keys.site(site), "job", id],
    enabled: !!id,
    queryFn: () => apiFetch<SiteJob>(`${one(site)}/jobs/${id}`),
    refetchInterval: (q) => (q.state.data?.status === "running" || !q.state.data ? 1000 : false),
  });
  // Once, when the job ends: inside the query it would cancel its own answer.
  const status = job.data?.status;
  useEffect(() => {
    if (status && status !== "running") void qc.invalidateQueries({ queryKey: keys.site(site) });
  }, [status, site, qc]);
  return job;
}

export function useSiteActiveJobs(site: string) {
  return useQuery({
    queryKey: [...keys.site(site), "jobs"],
    queryFn: () => apiFetch<{ jobs: SiteJob[] }>(`${one(site)}/jobs`),
    refetchInterval: (q) => ((q.state.data?.jobs.length ?? 0) > 0 ? 2000 : false),
  });
}

export function useCreateSite() {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (b: { name: string; description: string }) => apiFetch<Site>(root, json("POST", b)),
    onSuccess: invalidate,
  });
}

export interface SiteUpdate {
  description?: string;
  owner_email?: string;
  internal_view?: boolean;
}

export function useUpdateSite(site: string) {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (b: SiteUpdate) => apiFetch<Site>(one(site), json("PATCH", b)),
    onSuccess: invalidate,
  });
}

async function noContent(path: string, method: string) {
  const res = await apiFetchRaw(path, { method });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.detail ?? body.error ?? res.statusText);
  }
}

export function useDeleteSite(site: string) {
  const invalidate = useInvalidate();
  return useMutation({ mutationFn: () => noContent(one(site), "DELETE"), onSuccess: invalidate });
}

export interface PublishBody {
  environment: string;
  note: string;
  archive?: string;
  folder?: string;
  scope?: string;
  scope_id?: string;
  /** What the release says it came from: the file or folder's name. */
  source?: string;
}

export function usePublishSite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: PublishBody) => apiFetch<SiteJob>(`${one(site)}/publish`, json("POST", b)),
    onSuccess: invalidate,
  });
}

export interface CopyBody {
  url: string;
  environment: string;
  note: string;
  max_pages?: number;
  max_bytes?: number;
  form_map?: Record<string, string>;
}

export function useCopySite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: CopyBody) => apiFetch<SiteJob>(`${one(site)}/copy`, json("POST", b)),
    onSuccess: invalidate,
  });
}

/** The platform forms a copied page's forms can post to. */
export function useSiteForms(site: string) {
  return useQuery({
    queryKey: [...keys.site(site), "forms"],
    queryFn: () => apiFetch<{ forms: string[] }>(`${one(site)}/forms`),
  });
}

export function useStopSiteJob(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({ mutationFn: (id: string) => noContent(`${one(site)}/jobs/${id}/stop`, "POST"), onSuccess: invalidate });
}

export interface PromoteBody {
  from?: string;
  to: string;
  release?: number;
  reason?: string;
}

export function usePromoteSite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: PromoteBody) => apiFetch<SitePromoted>(`${one(site)}/promote`, json("POST", b)),
    onSuccess: invalidate,
  });
}

export function useRollbackSite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: { environment: string; release: number }) => apiFetch<SitePromoted>(`${one(site)}/rollback`, json("POST", b)),
    onSuccess: invalidate,
  });
}

export function useAddSiteDomain(site: string) {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (b: { hostname: string; environment: string; requires_approval: boolean }) =>
      apiFetch<SiteDomain>(`${one(site)}/domains`, json("POST", b)),
    onSuccess: invalidate,
  });
}

export function useRemoveSiteDomain(site: string) {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (hostname: string) => noContent(`${one(site)}/domains/${encodeURIComponent(hostname)}`, "DELETE"),
    onSuccess: invalidate,
  });
}

export function useGrantSite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: { email: string; level: SiteGrant["level"]; notify: boolean }) =>
      apiFetch<SiteGrant>(`${one(site)}/access`, json("PUT", { email: b.email, level: b.level, notify: b.notify })),
    onSuccess: invalidate,
  });
}

export function useRevokeSite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (email: string) => noContent(`${one(site)}/access?email=${encodeURIComponent(email)}`, "DELETE"),
    onSuccess: invalidate,
  });
}

/**
 * Where each environment's latest approval stands, read from the Approvals
 * queue: what the banners on the Environments tab say. A request already carried
 * out says nothing more.
 */
export function useApprovalForSite(site: Site): (ApprovalRequest & { target: string })[] {
  const { data } = useApprovals();
  const newest = new Map<string, ApprovalRequest & { target: string }>();
  for (const r of data ?? []) {
    if (r.subject_kind !== "site_release" || r.site_name !== site.name || !r.target) continue;
    const held = newest.get(r.target);
    if (!held || r.created_at > held.created_at) newest.set(r.target, r as ApprovalRequest & { target: string });
  }
  return [...newest.values()].filter((r) => {
    const served = site.environments.find((e) => e.name === r.target)?.release;
    return r.status === "pending" || r.status === "denied" || (r.status === "approved" && served !== r.version);
  });
}

/** Where an application answers: its page origin and the domains mapped to it. */
export function useApplicationAddresses(application: string) {
  return useQuery({
    queryKey: ["application-addresses", application],
    queryFn: () => apiFetch<{ page_origin: string; domains: string[] }>(`/application-addresses/${encodeURIComponent(application)}`),
    retry: false,
  });
}
