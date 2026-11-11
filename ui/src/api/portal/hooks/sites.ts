import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";
import { useApprovals, type ApprovalRequest } from "./approvals";

// Sites (#2559, #2604): agents and the people who manage a site work in dev;
// people use the environments a release was promoted to. Each environment is
// private (a sign-in, and only the people and personas it is shared with) or
// public, answers at <site>--<env> on the pages domain and at any domain added
// to it, and one environment may hold the site's main address. The server
// decides who may do what; each site reports the caller's permissions so the
// page hides what they would be refused.

/**
 * A site's name: one DNS label short enough that <site>--dev still fits in 63
 * characters, never "xn" (whose environments would read as an encoded
 * international name), with no doubled or trailing hyphen.
 */
export const SITE_NAME_RE = /^(?!xn$)(?!.*--)(?!.*-$)[a-z][a-z0-9-]{0,57}$/;
/** The working copy: every publish, copy and edit lands here. */
export const DEV_ENVIRONMENT = "dev";

export interface SiteEnvironment {
  name: string;
  release: number;
  requires_approval: boolean;
  /** Opens for anyone; false asks for a sign-in and a share. dev is never public. */
  public: boolean;
  /** Everyone signed in whose persona is not external can open it. */
  internal_view: boolean;
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
  /** The environment the main address serves, "" for none. */
  main_environment: string;
  environments: SiteEnvironment[];
  created_by: string;
  created_at: string;
  updated_at: string;
  /** The main address, "" when no environment holds it or there is no pages domain. */
  address: string;
  /** Each environment's address on the pages domain; the main address for the one holding it. */
  addresses: Record<string, string>;
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

/** One grant: a person (email) or a persona, never both. A persona grant is always view. */
export interface SiteGrant {
  email: string;
  persona: string;
  level: "manage" | "view";
  /** The environments a view grant opens; empty is every private environment. */
  environments: string[];
  granted_by: string;
  created_at: string;
}

export interface SiteAccessList {
  owner_email: string;
  grants: SiteGrant[];
  /** Every persona on the deployment, for sharing; sent to whoever manages the site. */
  personas: string[];
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

/** A publish always lands in dev, so it names no environment. */
export interface PublishBody {
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
    mutationFn: (b: { hostname: string; environment: string; requires_approval?: boolean; public?: boolean }) =>
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

export interface GrantBody {
  email?: string;
  persona?: string;
  level: SiteGrant["level"];
  environments?: string[];
  notify?: boolean;
}

export function useGrantSite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: GrantBody) => apiFetch<SiteGrant>(`${one(site)}/access`, json("PUT", b)),
    onSuccess: invalidate,
  });
}

/** Who a grant names, as the DELETE query carries it. */
export type GrantWho = { email: string } | { persona: string };

export function useRevokeSite(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (who: GrantWho) => {
      const q = "email" in who ? `email=${encodeURIComponent(who.email)}` : `persona=${encodeURIComponent(who.persona)}`;
      return noContent(`${one(site)}/access?${q}`, "DELETE");
    },
    onSuccess: invalidate,
  });
}

export interface EnvironmentBody {
  requires_approval?: boolean;
  public?: boolean;
  internal_view?: boolean;
  reason?: string;
}

export interface EnvironmentChanged {
  site: Site;
  status: "done" | "awaiting_approval";
  approval?: ApprovalRequest;
}

/** Creates an environment, or changes its access or approval rule. */
export function useSetSiteEnvironment(site: string) {
  const invalidate = useInvalidate();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, ...b }: EnvironmentBody & { name: string }) =>
      apiFetch<EnvironmentChanged>(`${one(site)}/environments/${encodeURIComponent(name)}`, json("PUT", b)),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["approvals"] });
      return invalidate();
    },
  });
}

/** Gives the main address to an environment, or to none with "". */
export function useSetMainEnvironment(site: string) {
  const invalidate = useInvalidate();
  return useMutation({
    mutationFn: (environment: string) => apiFetch<Site>(`${one(site)}/main-environment`, json("PUT", { environment })),
    onSuccess: invalidate,
  });
}

/**
 * Where each environment's latest approval stands, read from the Approvals
 * queue: what the banners on the Environments tab say, one per environment and
 * purpose (a promote, or making the environment public). A request already
 * carried out says nothing more.
 */
export function useApprovalForSite(site: Site): (ApprovalRequest & { target: string })[] {
  const { data } = useApprovals();
  const newest = new Map<string, ApprovalRequest & { target: string }>();
  for (const r of data ?? []) {
    if (r.subject_kind !== "site_release" || r.site_name !== site.name || !r.target) continue;
    const key = `${r.purpose}:${r.target}`;
    const held = newest.get(key);
    if (!held || r.created_at > held.created_at) newest.set(key, r as ApprovalRequest & { target: string });
  }
  return [...newest.values()].filter((r) => {
    const env = site.environments.find((e) => e.name === r.target);
    if (r.status === "pending" || r.status === "denied") return true;
    if (r.status !== "approved") return false;
    return r.purpose === "site_public" ? !!env && !env.public : env?.release !== r.version;
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
