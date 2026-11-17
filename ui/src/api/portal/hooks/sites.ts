import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";
import type { ApprovalRequest } from "./approvals";
import type { EnvironmentStatus } from "./siteOps";
import type { Routing } from "./siteRouting";

// Sites (#2559, #2604, #2617, #2619): agents and the people who manage a site
// work in dev; people use the environments a release was promoted to, one step
// at a time, in the site's stored order (dev first, prod last). Every address
// on the pages domain asks for a sign-in and opens for whoever the environment
// is shared with; only a domain added to an environment and set public answers
// anyone. prod holds the main address of a new site. The server
// decides who may do what; each site reports the caller's permissions so the
// page hides what they would be refused.

/** The working copy: every publish, copy and edit lands here. */
export const DEV_ENVIRONMENT = "dev";

export interface SiteEnvironment {
  name: string;
  release: number;
  requires_approval: boolean;
  /** The domains added to it that answer anyone (#2619); every other address asks for a sign-in. */
  public_domains: string[];
  /** Everyone signed in whose persona is not external can open it. */
  internal_view: boolean;
  /** How many releases it keeps (#2602): its last N distinct releases; 0 keeps every one. */
  keep_releases: number;
  /** True when keep_releases is the deployment's default rather than its own. */
  keep_releases_default: boolean;
  /** How many releases the limit keeps for it now. */
  releases_kept: number;
  /** What every address answers (#2631, #2632): online serves the release. dev is always online. */
  status?: EnvironmentStatus;
  /** An offline environment's message, and when its visitors are told it is back. */
  message?: string;
  back_at?: string;
  /** A redirecting environment's target; keep_path keeps the request's path. */
  redirect_to?: string;
  keep_path?: boolean;
  status_changed_by?: string;
  status_changed_at?: string;
  /** An agent's moves into it go ahead without a person's decision (#2643); an administrator's setting. */
  agents_may_promote?: boolean;
  /** Why the release it serves could not be drawn as its tile (#2664), as the tile worker recorded it. */
  tile_failure?: string;
  updated_by?: string;
  updated_at: string;
}

export interface SiteDomain {
  hostname: string;
  site_id?: string;
  site?: string;
  environment?: string;
  application?: string;
  /** Answers anyone at this hostname (#2619); a domain is private unless set public. */
  public: boolean;
  /** The environment's canonical domain (#2687); the redirect domains send visitors here. */
  primary?: boolean;
  /** serve (the release is served here) or redirect (a permanent redirect to the primary). */
  mode?: DomainMode;
  /** The primary a redirect domain sends visitors to. */
  redirects_to?: string;
  created_by: string;
  created_at: string;
  routing?: Routing; // the routing script's status (#2659); absent when no script is chosen
}

export type DomainMode = "serve" | "redirect";

/** What adding a domain or changing its access did: done, or waiting on Approvals. */
export interface DomainResult extends SiteDomain {
  status: "done" | "awaiting_approval";
  approval?: ApprovalRequest;
}

/** Whether an environment answers anyone at one of its domains. */
export function answersAnyone(env: Pick<SiteEnvironment, "public_domains">): boolean {
  return (env.public_domains ?? []).length > 0;
}

/** dev is always first and prod always last: neither moves (#2617). */
export const PROD_ENVIRONMENT = "prod";

/** What a new or moved environment can go after (#2617): every one in the order but prod and itself. */
export function placeChoices(order: string[], name: string): string[] {
  const out = order.filter((n) => n !== PROD_ENVIRONMENT && n !== name);
  return out.length > 0 ? out : [DEV_ENVIRONMENT];
}

/** The environment immediately before name in the site's order: the one a promotion to it takes from. */
export function previousEnvironment(site: Pick<Site, "environments">, name: string): SiteEnvironment | undefined {
  const i = site.environments.findIndex((e) => e.name === name);
  return i > 0 ? site.environments[i - 1] : undefined;
}

export interface SitePermissions {
  manage: boolean;
  own: boolean;
  delete: boolean;
}

/** A managed-resource folder a site serves at an address on every environment (#2591). */
export interface SiteMedia {
  /** The address, normalized to "/media/". */
  prefix: string;
  scope: "global" | "user" | "persona";
  scope_id: string;
  folder: string;
  created_by: string;
  created_at: string;
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
  /** The production address (#2658): public domain, other domain, pages address; "" for none. */
  address: string;
  more_domains?: number; // other domains the environment holding it answers at
  /** Where people reach each environment (#2607): its first domain, else its pages address; what manage_site get answers. */
  addresses: Record<string, string>;
  /** Each environment's address on the pages domain; the main address for the one holding it, "" for none. */
  pages_addresses: Record<string, string>;
  /** Why the environments have no pages address (#2611): a two-character name; absent otherwise. */
  address_note?: string;
  domains: SiteDomain[];
  pages_routing?: Record<string, Routing>; // each pages address's routing status by environment (#2659)
  permissions: SitePermissions;
  /** The media folders it serves, outside its releases (#2591); never null. */
  media?: SiteMedia[];
  /** The longest environment name it can add (#2636). */
  environment_name_room?: number;
  /** Its last 24 hours over the environments the caller sees (#2647), on the Sites list. */
  traffic?: { requests_24h: number; p95_ms: number };
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
  /** The release this one was derived from, 0 or absent for a whole file set. */
  base?: number;
  /** True once the daily cleanup removed its files (#2602); it stays listed as history. */
  pruned?: boolean;
  pruned_at?: string;
  /**
   * What keeps it: environment names whose limit keeps it, "application <name>"
   * for an application release pinning it, "approval" for a pending request.
   * Empty for a removed release and for one the next cleanup removes.
   */
  kept_by: string[];
}

export interface SiteFile {
  path: string;
  sha256?: string;
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
  from_release: number;
  to_release: number;
  actor: string;
  /** What a status change said, or a rename's old name; whose decision moved it (#2643); a removed environment's (#2630). */
  detail?: string;
  decided_by?: "elicitation" | "approval" | "environment_setting";
  removed?: boolean;
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
  /** Replaces the whole list of media folders; [] removes every one. */
  media?: Pick<SiteMedia, "prefix" | "scope" | "scope_id" | "folder">[];
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

/** The objects under a prefix of a bucket on an S3 connection the person can use (#2593). */
export interface BucketSource {
  connection: string;
  bucket: string;
  prefix?: string;
}

export interface PublishBody {
  note: string;
  archive?: string;
  folder?: string;
  /** Publish from a bucket prefix: one of archive, folder or from_bucket. */
  from_bucket?: BucketSource;
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

export interface DomainBody {
  hostname: string;
  environment: string;
  /** Sets whether the environment requires approval, whether the domain creates it or it exists. */
  requires_approval?: boolean;
  /** Adds it public: the owner's or an administrator's, through Approvals where the environment requires approval. */
  public?: boolean;
  reason?: string;
  /** Where an environment the domain creates goes (#2617). */
  after?: string;
  /** Adds it as its environment's primary, or set to redirect to the primary (#2687). */
  primary?: boolean;
  mode?: DomainMode;
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
  internal_view?: boolean;
  /** Administrators only (#2643). */
  agents_may_promote?: boolean;
  /** Takes it offline, redirects it or brings it online (#2631, #2632). */
  status?: EnvironmentStatus;
  message?: string;
  back_at?: string;
  redirect_to?: string;
  keep_path?: boolean;
  reason?: string;
  /** Renames an environment between dev and prod, alone (#2630). */
  rename_to?: string;
  /** Places a new environment, or moves one, immediately after this one (#2617). */
  after?: string;
  /** How many releases it keeps: a number (0 for every release) or "default". The owner and administrators only. */
  keep_releases?: number | "default";
}

export interface EnvironmentChanged {
  site: Site;
  status: "done" | "awaiting_approval";
  approval?: ApprovalRequest;
}

/** Creates an environment, moves it, or changes its settings. */
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

/** Where an application answers: its page origin and the domains mapped to it. */

export * from "./siteOps";
export * from "./siteDomains";
export * from "./siteRouting";
