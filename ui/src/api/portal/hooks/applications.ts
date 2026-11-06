import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { sortNames } from "@/lib/sortByName";
import { apiFetch, apiFetchRaw, ApiError } from "../client";

// Applications (#2411): one record naming what a system built on the platform
// is made of -- an application database schema, managed scripts, webhook
// sources and portal assets -- and the releases that pin each script's and
// asset's version. Everyone signed in reads; the owner and administrators edit
// the record and its parts; only administrators release, roll back, export and
// import. The server enforces every one of those rules; the page only hides
// the controls a caller would be refused.

/** The kinds of thing an application is made of. */
export type ApplicationPartKind = "script" | "asset" | "source";

export interface ApplicationPart {
  kind: ApplicationPartKind;
  id: string;
  added_by: string;
  added_at: string;
}

export interface Application {
  id: string;
  name: string;
  description: string;
  owner_email: string;
  connection: string;
  schema: string;
  /** The release in force, 0 before the first one. */
  current_release: number;
  created_by: string;
  created_at: string;
  updated_at: string;
  parts: ApplicationPart[];
}

/** One part's pinned version in a release. */
export interface ApplicationPin {
  kind: ApplicationPartKind;
  part_id: string;
  version: number;
}

/** A part whose pin a release changed, or would change. */
export interface ApplicationPinChange {
  kind: ApplicationPartKind;
  part_id: string;
  from: number;
  to: number;
}

export interface ApplicationChanges {
  parts: ApplicationPinChange[];
  applied: number[];
  reverted: number[];
  migrations_kept?: string;
}

export type ApplicationReleaseAction = "release" | "rollback" | "import";

export interface ApplicationRelease {
  number: number;
  action: ApplicationReleaseAction;
  migration_version: number;
  /** The release a rollback restored. */
  restored?: number;
  changes: ApplicationChanges;
  note?: string;
  created_by: string;
  created_at: string;
  pins: ApplicationPin[];
}

export interface ApplicationList {
  applications: Application[];
  total: number;
}

/** An application's page: where it stands and its releases, newest first. */
export interface ApplicationDetail {
  application: Application;
  current_release: ApplicationRelease | null;
  pending: ApplicationPinChange[];
  migrations?: { applied: number; pending: number[] };
  problems: string[];
  releases: ApplicationRelease[];
}

export interface ApplicationCreateInput {
  name: string;
  description: string;
  connection?: string;
  schema?: string;
}

/** The names a bundle refers to that may differ on the importing deployment. */
export interface ApplicationBundleNames {
  connections?: string[];
  secrets?: string[];
  sources?: string[];
  bot_checks?: string[];
}

/** The part of an exported bundle the import dialog reads. */
export interface ApplicationBundle {
  application?: { name?: string; description?: string };
  release?: number;
  names?: ApplicationBundleNames;
  [key: string]: unknown;
}

export interface ApplicationImportResult {
  application: string;
  created: boolean;
  release: ApplicationRelease | null;
  scripts: Record<string, string>;
  assets: Record<string, string>;
}

export const applicationsKey = ["applications"] as const;
export const applicationKey = (app: string) => [...applicationsKey, app] as const;

const BASE = "/applications";

function appPath(app: string): string {
  return `${BASE}/${encodeURIComponent(app)}`;
}

export function useApplications() {
  return useQuery({
    queryKey: applicationsKey,
    queryFn: () => apiFetch<ApplicationList>(BASE),
  });
}

export function useApplication(app: string) {
  return useQuery({
    queryKey: applicationKey(app),
    queryFn: () => apiFetch<ApplicationDetail>(appPath(app)),
    enabled: app !== "",
  });
}

/** One schema of an application database connection, and the application holding it. */
export interface ApplicationSchemaChoice {
  name: string;
  held_by?: string;
}

/** An application database connection the caller reaches, with its schemas (#2491). */
export interface ApplicationDatabaseChoice {
  connection: string;
  description?: string;
  schemas: ApplicationSchemaChoice[];
  /** Set when the schemas could not be read; a new name can still be entered. */
  problem?: string;
}

export function useApplicationDatabases(enabled = true) {
  return useQuery({
    queryKey: ["application-databases"],
    queryFn: () =>
      apiFetch<{ connections: ApplicationDatabaseChoice[] }>("/application-databases").then(
        (r) => r.connections,
      ),
    enabled,
  });
}

/** The rule an application's name follows, as the server checks it. */
export const APPLICATION_NAME_RE = /^[a-z][a-z0-9-]{0,62}$/;

export function useCreateApplication() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: ApplicationCreateInput) =>
      apiFetch<Application>(BASE, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: applicationsKey }),
  });
}

// Every write on one application changes its page and, through its parts or
// current release, the listing, so each invalidates the whole family.
function useApplicationWrite<I, O>(fn: (input: I) => Promise<O>) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: applicationsKey }),
  });
}

export function useUpdateApplication(app: string) {
  return useApplicationWrite((input: { description: string; owner_email?: string }) =>
    apiFetch<Application>(appPath(app), { method: "PATCH", body: JSON.stringify(input) }),
  );
}

export function useDeleteApplication(app: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => apiFetch<{ deleted: string }>(appPath(app), { method: "DELETE" }),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: applicationKey(app) });
      return queryClient.invalidateQueries({ queryKey: applicationsKey });
    },
  });
}

export function useAddApplicationPart(app: string) {
  return useApplicationWrite((input: { kind: ApplicationPartKind; id: string }) =>
    apiFetch<Application>(`${appPath(app)}/parts`, { method: "POST", body: JSON.stringify(input) }),
  );
}

export function useRemoveApplicationPart(app: string) {
  return useApplicationWrite((input: { kind: ApplicationPartKind; id: string }) =>
    apiFetch<Application>(
      `${appPath(app)}/parts/${encodeURIComponent(input.kind)}/${encodeURIComponent(input.id)}`,
      { method: "DELETE" },
    ),
  );
}

export function useReleaseApplication(app: string) {
  return useApplicationWrite((input: { note?: string }) =>
    apiFetch<ApplicationRelease>(`${appPath(app)}/release`, {
      method: "POST",
      body: JSON.stringify(input.note ? { note: input.note } : {}),
    }),
  );
}

export function useRollbackApplication(app: string) {
  return useApplicationWrite((input: { to_release?: number }) =>
    apiFetch<ApplicationRelease>(`${appPath(app)}/rollback`, {
      method: "POST",
      body: JSON.stringify(input.to_release ? { to_release: input.to_release } : {}),
    }),
  );
}

export function useImportApplication() {
  return useApplicationWrite((input: { bundle: ApplicationBundle; map: Record<string, string> }) =>
    apiFetch<ApplicationImportResult>(`${BASE}/import`, {
      method: "POST",
      body: JSON.stringify(input),
    }),
  );
}

/**
 * downloadApplicationExport fetches a release's bundle and hands it to the
 * browser as a file. The route answers with an attachment; reading it through
 * the client keeps the API-key header and CSRF handling the other calls use.
 */
export async function downloadApplicationExport(app: string, release?: number): Promise<void> {
  const query = release ? `?release=${release}` : "";
  const res = await apiFetchRaw(`${appPath(app)}/export${query}`, { method: "GET" });
  if (!res.ok) {
    const body = (await res.json().catch(() => ({ detail: res.statusText }))) as {
      detail?: string;
    };
    throw new ApiError(res.status, body.detail || res.statusText, body);
  }
  const filename = attachmentName(res.headers.get("Content-Disposition")) ?? `${app}-application.json`;
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

/** attachmentName reads the filename a Content-Disposition header names. */
export function attachmentName(header: string | null): string | undefined {
  if (!header) return undefined;
  const match = header.match(/filename="?([^";]+)"?/i);
  return match?.[1];
}

/** bundleNames lists every name a bundle refers to, sorted and once. */
export function bundleNames(bundle: ApplicationBundle): string[] {
  const n = bundle.names ?? {};
  const all = [
    ...(n.connections ?? []),
    ...(n.secrets ?? []),
    ...(n.sources ?? []),
    ...(n.bot_checks ?? []),
  ];
  return sortNames(new Set(all));
}
