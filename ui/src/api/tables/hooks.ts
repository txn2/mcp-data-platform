import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@/stores/auth";
import { applyCsrfHeader } from "@/api/csrf";
import type {
  ScratchTable,
  ScratchTableList,
  ScratchTableQuery,
  TableConnectionList,
  TableRegistration,
  TableRegistrationList,
  TableSourceKind,
} from "./types";

// TableApiError carries the status alongside the message so a caller can tell
// "you cannot reach this connection" (403) from "that name is taken" (409).
//
// type is the RFC 9457 problem type. It is "about:blank" for most refusals and
// names the problem for the ones a surface offers a next step on, which is
// what lets a control be keyed off the refusal rather than off its prose.
export class TableApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
    public type: string = "about:blank",
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
  return new TableApiError(res.status, body.detail || body.error || res.statusText, body.type);
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
    // repair asks for a corrected version of the file to be saved and
    // registered. It is the second submission of the form: the first is
    // refused with what is wrong, and that refusal is what offers this.
    mutationFn: (body: { connection: string; table_name?: string; repair?: boolean }) =>
      tableFetch<TableRegistration>(basePath(kind, id), {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: (registration) => {
      void qc.invalidateQueries({ queryKey: tablesKey(kind, id) });
      invalidateScratchTables(qc);
      // A registration that corrected the file wrote a version of it, and the
      // version panel sits on the same page as the answer saying so. Without
      // this it keeps showing the version before the correction as current,
      // and the file's own size and updated date stay behind with it.
      if (registration.repaired) {
        invalidateCorrectedSource(qc, kind, id);
      }
    },
    // The correction is written before the last checks and before the DDL, so
    // a refusal or a failure can follow one and the file stays changed either
    // way. The server says so with its own problem type rather than in prose,
    // and the panels showing the file are as far behind here as they would be
    // after a success.
    onError: (error) => {
      if (error instanceof TableApiError && error.type === PROBLEM_FILE_CORRECTED) {
        invalidateCorrectedSource(qc, kind, id);
      }
    },
  });
}

// PROBLEM_FILE_CORRECTED is the RFC 9457 type on an answer whose registration
// did not happen but whose file changed anyway (codeFileCorrected in
// internal/httpserver/tablehttp).
const PROBLEM_FILE_CORRECTED = "urn:mcp-data-platform:problem:file-corrected";

// invalidateCorrectedSource refreshes what a correction changed about the file
// itself: its version trail and the record carrying its size, type, and head.
// The queries differ per kind because the trails do -- a managed resource
// records a revision, a portal asset records a version.
function invalidateCorrectedSource(
  qc: ReturnType<typeof useQueryClient>,
  kind: TableSourceKind,
  id: string,
): void {
  if (kind === "resource") {
    // One prefix: the detail read and the version trail are both under it.
    void qc.invalidateQueries({ queryKey: ["resources"] });
    return;
  }
  void qc.invalidateQueries({ queryKey: ["asset", id] });
  void qc.invalidateQueries({ queryKey: ["asset-content", id] });
  void qc.invalidateQueries({ queryKey: ["asset-versions", id] });
  void qc.invalidateQueries({ queryKey: ["assets"] });
}

export function useUnregisterTable(kind: TableSourceKind, id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (registrationID: string) =>
      tableFetch<void>(`${basePath(kind, id)}/${registrationID}`, { method: "DELETE" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: tablesKey(kind, id) });
      invalidateScratchTables(qc);
    },
  });
}

// invalidateScratchTables refreshes the cross-source listing after a
// registration is added or dropped. It is called from the per-source
// mutations because those are the only two doors: the listing has no register
// action of its own, and its unregister goes through the source's own route.
function invalidateScratchTables(qc: ReturnType<typeof useQueryClient>): void {
  void qc.invalidateQueries({ queryKey: ["scratch-tables"] });
  void qc.invalidateQueries({ queryKey: ["scratch-table"] });
}

// --- the cross-source listing (#1472) ---

// scratchTablesKey keys the listing by its facets, so a filter change is a new
// query rather than a refetch of the same one.
const scratchTablesKey = (params: ScratchTableQuery) => ["scratch-tables", params] as const;

// scratchTableQueryString renders the facets a caller named. A facet left out
// is left out of the URL: the server's default is the answer, and sending an
// empty value would key the cache on a distinction the server does not make.
function scratchTableQueryString(params: ScratchTableQuery): string {
  const search = new URLSearchParams();
  if (params.page && params.page > 1) search.set("page", String(params.page));
  if (params.perPage) search.set("per_page", String(params.perPage));
  if (params.connection) search.set("connection", params.connection);
  if (params.kind) search.set("kind", params.kind);
  if (params.q) search.set("q", params.q);
  const rendered = search.toString();
  return rendered ? `?${rendered}` : "";
}

// useScratchTables lists every registration this person may see: the ones on
// the connections their persona is granted, whichever file each was built
// over. An administrator sees all of them.
export function useScratchTables(params: ScratchTableQuery) {
  return useQuery({
    queryKey: scratchTablesKey(params),
    queryFn: () => tableFetch<ScratchTableList>(`/api/v1/tables${scratchTableQueryString(params)}`),
  });
}

// useScratchTable reads one registration by id, which is what the detail route
// opens. A registration on a connection the caller does not reach answers as a
// 404, the same as one that does not exist.
export function useScratchTable(id: string | undefined) {
  return useQuery({
    queryKey: ["scratch-table", id],
    queryFn: () => tableFetch<ScratchTable>(`/api/v1/tables/${encodeURIComponent(id as string)}`),
    enabled: Boolean(id),
  });
}
