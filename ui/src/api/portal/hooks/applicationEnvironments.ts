import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import {
  appPath,
  applicationsKey,
  useApplicationWrite,
  type Application,
  type ApplicationRelease,
} from "./applications";

// An application's environments and variables (#2696): promote and roll back
// one environment, add one, and read and write NAME=value per environment.

/** NAME=value for one environment, or the default when environment is "" (#2696). */
export interface ApplicationVariable {
  environment: string;
  name: string;
  value: string;
  updated_by: string;
  updated_at: string;
}

/** One environment's schema as the application's page shows it (#2709). */
export interface ApplicationEnvironmentSchema {
  environment: string;
  schema: string;
  /** The highest migration applied in it. */
  migration_version: number;
  /** What its next release or promote would apply. */
  pending: number[];
  /** Another environment running in the same schema; absent when it is its own. */
  shared?: string;
  problem?: string;
}

/** How many of an application's files name its schema or roles (#2709). */
export interface ApplicationPrepareCounts {
  scripts: number;
  libraries: number;
  migrations: number;
}

/** What a promote did. */
export interface ApplicationPromoted {
  from: string;
  to: string;
  release: number;
  replaced: number;
  /** The migrations it applied to the target's schema (#2709). */
  applied: number[];
}

/** One file prepare changes (#2709). */
export interface ApplicationPrepareFile {
  kind: "script" | "library" | "migration";
  id: string;
  name: string;
  version: number;
  /** How many names of the schema or its roles it holds. */
  names: number;
  before: string;
  after: string;
  /** The recorded runs the save gate replayed against the new text. */
  replayed: string[];
  differences: unknown[];
  /** Why the save gate refuses the new text. */
  refusal?: string;
  /** The version saved. */
  saved?: number;
}

/** What prepare changes in an application (#2709). */
/** A schema name inside a quoted SQL value: data prepare leaves as written (#2709). */
export interface ApplicationPrepareValue {
  kind: string;
  name: string;
  line: number;
  text: string;
}

export interface ApplicationPreparePlan {
  application: string;
  schema: string;
  files: ApplicationPrepareFile[];
  values: ApplicationPrepareValue[];
  counts: ApplicationPrepareCounts;
  ready: boolean;
  saved: boolean;
}

const variablesKey = (app: string) => [...applicationsKey, app, "variables"] as const;

/** The application's variables, for its owner and administrators (#2696). */
export function useApplicationVariables(app: string, enabled: boolean) {
  return useQuery({
    queryKey: variablesKey(app),
    queryFn: () => apiFetch<{ variables: ApplicationVariable[] }>(`${appPath(app)}/variables`),
    enabled: enabled && app !== "",
  });
}

function envPath(app: string, env: string): string {
  return `${appPath(app)}/environments/${encodeURIComponent(env)}`;
}

export function usePromoteEnvironment(app: string) {
  return useApplicationWrite((input: { environment: string }) =>
    apiFetch<ApplicationPromoted>(`${envPath(app, input.environment)}/promote`, { method: "POST" }),
  );
}

export function useRollbackEnvironment(app: string) {
  return useApplicationWrite((input: { environment: string; to_release?: number }) =>
    apiFetch<ApplicationRelease>(`${envPath(app, input.environment)}/rollback`, {
      method: "POST",
      body: JSON.stringify(input.to_release ? { to_release: input.to_release } : {}),
    }),
  );
}

export function useAddEnvironment(app: string) {
  return useApplicationWrite((input: { name: string; after: string; requires_approval: boolean; schema?: string }) =>
    apiFetch<Application>(`${appPath(app)}/environments`, { method: "POST", body: JSON.stringify(input) }),
  );
}

export function useRemoveEnvironment(app: string) {
  return useApplicationWrite((input: { environment: string }) =>
    apiFetch<Application>(envPath(app, input.environment), { method: "DELETE" }),
  );
}

const prepareKey = (app: string) => [...applicationsKey, app, "prepare"] as const;

/** What prepare would change, each script replayed by the save gate (#2709). */
export function usePreparePlan(app: string, enabled: boolean) {
  return useQuery({
    queryKey: prepareKey(app),
    queryFn: () => apiFetch<ApplicationPreparePlan>(`${appPath(app)}/prepare`),
    enabled: enabled && app !== "",
  });
}

/** preparePlanOf is the plan a refused save carries, null for another error. */
export function preparePlanOf(e: unknown): ApplicationPreparePlan | null {
  const body = (e as { body?: { plan?: ApplicationPreparePlan } } | null)?.body;
  return body?.plan ?? null;
}

/** Saves prepare's changes as new versions (#2709); administrators. */
export function useSavePrepare(app: string) {
  return useApplicationWrite(() => apiFetch<ApplicationPreparePlan>(`${appPath(app)}/prepare`, { method: "POST" }));
}

export function useSetVariable(app: string) {
  return useApplicationWrite((input: { name: string; environment: string; value: string }) =>
    apiFetch<{ variables: ApplicationVariable[] }>(`${appPath(app)}/variables/${encodeURIComponent(input.name)}`, {
      method: "PUT",
      body: JSON.stringify({ environment: input.environment, value: input.value }),
    }),
  );
}

export function useDeleteVariable(app: string) {
  return useApplicationWrite((input: { name: string; environment: string }) => {
    const query = input.environment ? `?environment=${encodeURIComponent(input.environment)}` : "";
    return apiFetch<{ variables: ApplicationVariable[] }>(
      `${appPath(app)}/variables/${encodeURIComponent(input.name)}${query}`,
      { method: "DELETE" },
    );
  });
}
