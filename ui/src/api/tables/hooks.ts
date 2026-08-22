import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@/stores/auth";
import { applyCsrfHeader } from "@/api/csrf";
import type {
  TableConnectionList,
  TableRegistration,
  TableRegistrationList,
  TableSourceKind,
} from "./types";

// TableApiError carries the status alongside the message so a caller can tell
// "you cannot reach this connection" (403) from "that name is taken" (409).
export class TableApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
  ) {
    super(detail);
    this.name = "TableApiError";
  }
}

// tableHeaders builds the request headers: the API key when that is how this
// session authenticates, a content type for anything carrying a body, and the
// CSRF token a cookie-authenticated mutation needs.
function tableHeaders(init?: RequestInit): Record<string, string> {
  const { apiKey, authMethod } = useAuthStore.getState();
  const headers: Record<string, string> = { ...(init?.headers as Record<string, string>) };
  if (authMethod === "apikey" && apiKey) {
    headers["X-API-Key"] = apiKey;
  }
  if (init?.method && init.method !== "GET") {
    headers["Content-Type"] = headers["Content-Type"] ?? "application/json";
  }
  applyCsrfHeader(headers, init?.method);
  return headers;
}

// tableError turns a refusal into the error a caller renders. The platform's
// refusals name what to do next, so the detail is carried through as written.
async function tableError(res: Response): Promise<TableApiError> {
  if (res.status === 401) {
    useAuthStore.getState().expireSession();
  }
  const body = await res.json().catch(() => ({ detail: res.statusText }));
  return new TableApiError(res.status, body.detail || body.error || res.statusText);
}

// tableFetch talks to the table routes, which live beside the resources and
// portal APIs rather than under either, so the path is absolute.
async function tableFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { ...init, headers: tableHeaders(init), credentials: "include" });
  if (!res.ok) {
    throw await tableError(res);
  }
  // A 204 carries no body; a delete has nothing to say beyond succeeding.
  if (res.status === 204) {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}

// basePath is the one place the two kinds' routes differ.
function basePath(kind: TableSourceKind, id: string): string {
  return kind === "asset" ? `/api/v1/portal/assets/${id}/tables` : `/api/v1/resources/${id}/tables`;
}

const tablesKey = (kind: TableSourceKind, id: string) => ["tables", kind, id];

// useTableRegistrations lists the tables registered over one file. It is
// disabled without an id so a panel can mount before its record loads.
export function useTableRegistrations(kind: TableSourceKind, id: string | undefined) {
  return useQuery({
    queryKey: tablesKey(kind, id ?? ""),
    queryFn: () => tableFetch<TableRegistrationList>(basePath(kind, id as string)),
    enabled: Boolean(id),
  });
}

// useTableConnections lists the connections this person can register onto.
// It is the picker's only source, so a connection it offers is one the
// registrar accepts; an empty list means no connection here can hold a table.
export function useTableConnections(enabled = true) {
  return useQuery({
    queryKey: ["table-connections"],
    queryFn: () => tableFetch<TableConnectionList>("/api/v1/table-connections"),
    enabled,
    // Connections change when an administrator edits configuration, not while
    // somebody is filling in a form.
    staleTime: 5 * 60 * 1000,
  });
}

export function useRegisterTable(kind: TableSourceKind, id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { connection: string; table_name?: string }) =>
      tableFetch<TableRegistration>(basePath(kind, id), {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: tablesKey(kind, id) });
    },
  });
}

export function useUnregisterTable(kind: TableSourceKind, id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (registrationID: string) =>
      tableFetch<void>(`${basePath(kind, id)}/${registrationID}`, { method: "DELETE" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: tablesKey(kind, id) });
    },
  });
}
