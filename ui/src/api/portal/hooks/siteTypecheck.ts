import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// Site type checks (#2793): a site whose _build.json asks for one has each
// release checked with TypeScript after it publishes. A check, not a gate:
// the release is served whatever it finds.

export type TypecheckStatus =
  | "queued"
  | "running"
  | "clean"
  | "errors"
  | "over_limit"
  | "unsupported"
  | "failed";

export interface TypecheckDiagnostic {
  /** Empty, with line and column 0, for a diagnostic about the project rather than a place in a file (TS18003). */
  file: string;
  line: number;
  column: number;
  category: string;
  code: string;
  message: string;
}

export interface TypecheckResult {
  status: TypecheckStatus;
  diagnostics: TypecheckDiagnostic[];
  /** Packages the code imports that the lockfile does not lock. */
  missing: string[];
  /** Packages the lockfile names with no integrity hash, which are not fetched. */
  unverifiable: string[];
  packages: number;
  fetched: number;
  lockfile?: string;
  message?: string;
  duration_ms: number;
  /** The part of duration_ms spent preparing the compiler before it read a file. */
  compile_ms?: number;
}

export interface TypecheckCheck {
  id: number;
  release: number;
  environment: string;
  tsconfig: string;
  status: TypecheckStatus;
  result?: TypecheckResult;
  requested_by?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  /** How many queued checks run before this one. */
  ahead?: number;
}

export interface TypecheckView {
  release: number;
  /** Null when no check has run. */
  check: TypecheckCheck | null;
}

export interface TypecheckSummary {
  status: TypecheckStatus;
  errors: number;
}

export interface TypecheckSummaries {
  /** The newest check of each release, by release number. */
  releases: Record<string, TypecheckSummary>;
}

const one = (site: string) => `/sites/${encodeURIComponent(site)}`;
const key = (site: string) => ["sites", site, "typecheck"] as const;

/** isPending is a check that has not finished. */
export function isPending(status: TypecheckStatus | undefined): boolean {
  return status === "queued" || status === "running";
}

export function useSiteTypechecks(site: string, enabled = true) {
  return useQuery({
    enabled,
    queryKey: [...key(site), "summaries"],
    queryFn: () => apiFetch<TypecheckSummaries>(`${one(site)}/typechecks`),
    refetchInterval: (q) =>
      Object.values(q.state.data?.releases ?? {}).some((s) => isPending(s.status)) ? 3000 : false,
  });
}

export function useReleaseTypecheck(site: string, release: number) {
  return useQuery({
    queryKey: [...key(site), release],
    queryFn: () => apiFetch<TypecheckView>(`${one(site)}/releases/${release}/typecheck`),
    // A check runs in the background: poll while it waits or runs.
    refetchInterval: (q) => (isPending(q.state.data?.check?.status) ? 2000 : false),
  });
}

export function useRetypecheck(site: string, release: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch<TypecheckView>(`${one(site)}/releases/${release}/typecheck`, { method: "POST" }),
    onSuccess: (view) => {
      qc.setQueryData([...key(site), release], view);
      void qc.invalidateQueries({ queryKey: key(site) });
    },
  });
}
