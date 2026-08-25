import { useQueries, useQuery } from "@tanstack/react-query";
import { apiFetch, apiFetchAt, ApiError } from "@/api/admin/client";
import type { APICatalogSpec } from "@/api/admin/hooks/catalogs";
import type {
  APIConnectionList,
  APIOperationDetail,
  APIOperationList,
  APIOperationSummary,
  APISpecOperationList,
} from "./types";

// The operation browser's data access, both halves in one module because they
// serve one page (#1478).
//
// The caller-scoped routes sit beside the REST gateway rather than under
// /api/v1/portal, because their second reader is a plain HTTP client composing
// an invoke body. The operator-scoped ones are catalog routes. Both go through
// the admin client's fetch, so the session credential and the 401 recovery are
// one implementation rather than three.

const APIS_BASE = "/api/v1/apis";

/**
 * encodeOperationID escapes an id for a path segment. An operation with no
 * declared operationId carries a synthesized one ("GET /things/{id}"), which
 * has a space, slashes and braces in it.
 */
export function encodeOperationID(operationID: string): string {
  return encodeURIComponent(operationID);
}

/** useAPIConnections lists the api-kind connections this caller reaches. */
export function useAPIConnections(enabled = true) {
  return useQuery({
    queryKey: ["api-browse", "connections"],
    queryFn: () => apiFetchAt<APIConnectionList>(APIS_BASE, ""),
    enabled,
    // Connections change when an operator edits configuration, not while
    // somebody is reading an operation.
    staleTime: 5 * 60 * 1000,
  });
}

/**
 * useAPIOperations lists one connection's operations, with the connection's
 * upstream root and auth mode alongside them. Disabled without a connection so
 * the page can mount before the listing picks one.
 */
export function useAPIOperations(connection: string | undefined) {
  return useQuery({
    queryKey: ["api-browse", "operations", connection],
    queryFn: () =>
      apiFetchAt<APIOperationList>(
        APIS_BASE,
        `/${encodeURIComponent(connection as string)}/operations`,
      ),
    enabled: Boolean(connection),
    staleTime: 5 * 60 * 1000,
  });
}

/**
 * useAPIOperation reads one operation of one connection in full. spec
 * disambiguates an id defined by more than one component spec, which the index
 * always knows because every row carries its spec.
 */
export function useAPIOperation(
  connection: string | undefined,
  operationID: string | undefined,
  spec?: string,
) {
  return useQuery({
    queryKey: ["api-browse", "operation", connection, operationID, spec ?? ""],
    queryFn: () =>
      apiFetchAt<APIOperationDetail>(
        APIS_BASE,
        `/${encodeURIComponent(connection as string)}/operations/` +
          `${encodeOperationID(operationID as string)}${spec ? `?spec=${encodeURIComponent(spec)}` : ""}`,
      ),
    enabled: Boolean(connection && operationID),
  });
}

/** useCatalogSpecs lists a catalog's component specs. */
export function useCatalogSpecs(catalogID: string | undefined) {
  return useQuery({
    queryKey: ["api-catalogs", catalogID, "specs"],
    queryFn: () =>
      apiFetch<{ specs?: APICatalogSpec[] }>(
        `/api-catalogs/${encodeURIComponent(catalogID as string)}/specs`,
      ),
    enabled: Boolean(catalogID),
  });
}

/**
 * FailedSpec is a spec missing from an index, and why.
 *
 * The two readings send an operator to different places, so they are kept
 * apart: `unparseable` is the route's 422 -- the stored content is not OpenAPI,
 * and the spec is what needs fixing. Anything else (the store was unreachable,
 * the spec was deleted between the listing and the read, a network blip) says
 * nothing about the content, and reporting it as malformed would send them to
 * edit a spec that is fine.
 */
export interface FailedSpec {
  name: string;
  unparseable: boolean;
}

/** UNPARSEABLE_STATUS is what the operations route answers for stored content
 * that no longer parses as OpenAPI (internal/admin/catalogapi/operations.go). */
const UNPARSEABLE_STATUS = 422;

/**
 * useCatalogOperations reads the operations of every named spec in a catalog,
 * one request per spec, and returns them as one index carrying each row's spec.
 *
 * A catalog's specs are separate documents that parse independently, so a spec
 * that cannot be read fails on its own rather than emptying the index: its
 * operations are absent and the rest are still readable.
 */
export function useCatalogOperations(catalogID: string | undefined, specNames: string[]) {
  const results = useQueries({
    queries: specNames.map((name) => ({
      queryKey: ["api-catalogs", catalogID, "specs", name, "operations"],
      queryFn: () =>
        apiFetch<APISpecOperationList>(
          `/api-catalogs/${encodeURIComponent(catalogID as string)}` +
            `/specs/${encodeURIComponent(name)}/operations`,
        ),
      enabled: Boolean(catalogID),
    })),
  });

  const operations: APIOperationSummary[] = [];
  const failedSpecs: FailedSpec[] = [];
  results.forEach((r, i) => {
    const name = specNames[i] as string;
    if (r.error) {
      failedSpecs.push({ name, unparseable: isUnparseable(r.error) });
      return;
    }
    for (const op of r.data?.operations ?? []) {
      operations.push({ ...op, spec: op.spec || name });
    }
  });

  return {
    operations,
    failedSpecs,
    isLoading: results.some((r) => r.isLoading),
  };
}

/** isUnparseable reports whether a refusal was about the spec's content. */
function isUnparseable(err: unknown): boolean {
  return err instanceof ApiError && err.status === UNPARSEABLE_STATUS;
}

/** useCatalogSpecOperation reads one operation of one stored spec in full. */
export function useCatalogSpecOperation(
  catalogID: string | undefined,
  specName: string | undefined,
  operationID: string | undefined,
) {
  return useQuery({
    queryKey: ["api-catalogs", catalogID, "specs", specName, "operations", operationID],
    queryFn: () =>
      apiFetch<APIOperationDetail>(
        `/api-catalogs/${encodeURIComponent(catalogID as string)}` +
          `/specs/${encodeURIComponent(specName as string)}` +
          `/operations/${encodeOperationID(operationID as string)}`,
      ),
    enabled: Boolean(catalogID && specName && operationID),
  });
}
