import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { appPath, applicationsKey, useApplicationWrite, type Application, type ApplicationRelease } from "./applications";

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

/** What a promote did. */
export interface ApplicationPromoted {
  from: string;
  to: string;
  release: number;
  replaced: number;
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
  return useApplicationWrite((input: { name: string; after: string; requires_approval: boolean }) =>
    apiFetch<Application>(`${appPath(app)}/environments`, { method: "POST", body: JSON.stringify(input) }),
  );
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
