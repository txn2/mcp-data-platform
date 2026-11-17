import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";
import type { DomainBody, DomainMode, DomainResult } from "./sites";

// A site's domains: adding one, its access (#2619), its role among its
// environment's domains (#2687) and removing it. Re-exported by ./sites.

const root = "/sites";
const one = (site: string) => `${root}/${encodeURIComponent(site)}`;
const json = (method: string, body: unknown) => ({ method, body: JSON.stringify(body) });

async function noContent(path: string, method: string) {
  const res = await apiFetchRaw(path, { method });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.detail ?? body.error ?? res.statusText);
  }
}

function useInvalidateSites() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ["sites"] });
}

export function useAddSiteDomain(site: string) {
  const invalidate = useInvalidateSites();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (b: DomainBody) => apiFetch<DomainResult>(`${one(site)}/domains`, json("POST", b)),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["approvals"] });
      return invalidate();
    },
  });
}

/** Sets a domain's role among its environment's domains (#2687): primary, and serve or redirect. */
export function useSetDomainRole(site: string) {
  const invalidate = useInvalidateSites();
  return useMutation({
    mutationFn: ({ hostname, ...b }: { hostname: string; primary?: boolean; mode?: DomainMode }) =>
      apiFetch<DomainResult>(`${one(site)}/domains/${encodeURIComponent(hostname)}`, json("PUT", b)),
    onSuccess: invalidate,
  });
}

/** Makes one domain public or private (#2619). */
export function useSetDomainAccess(site: string) {
  const invalidate = useInvalidateSites();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ hostname, ...b }: { hostname: string; public: boolean; reason?: string }) =>
      apiFetch<DomainResult>(`${one(site)}/domains/${encodeURIComponent(hostname)}`, json("PUT", b)),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["approvals"] });
      return invalidate();
    },
  });
}

export function useRemoveSiteDomain(site: string) {
  const invalidate = useInvalidateSites();
  return useMutation({
    mutationFn: (hostname: string) => noContent(`${one(site)}/domains/${encodeURIComponent(hostname)}`, "DELETE"),
    onSuccess: invalidate,
  });
}

