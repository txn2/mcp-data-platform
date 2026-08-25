import { http, HttpResponse } from "msw";
import { mockCatalogSpecs } from "../data/catalogs";
import {
  connectionOperationDetail,
  connectionOperations,
  connectionView,
  mockAPIConnections,
  operationDetailFromSpec,
  operationsFromSpec,
  specBasePath,
} from "../data/apis";

// The operation browser's routes (#1478): the caller-scoped ones beside the
// REST gateway, and the operator's two on the catalog surface.

const APIS_BASE = "/api/v1/apis";
const ADMIN_BASE = "/api/v1/admin";

function findConnection(name: string) {
  return mockAPIConnections.find((c) => c.name === name);
}

/** decodeId recovers an operation id from its path segment. A synthesized id
 * carries a space and slashes, so the client escapes it; whether the router
 * hands back the escaped or the unescaped form is not this fixture's business. */
function decodeId(raw: unknown): string {
  const value = String(raw);
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export const apiBrowseHandlers = [
  // The connections this caller reaches.
  http.get(APIS_BASE, () =>
    HttpResponse.json({ connections: mockAPIConnections.map(connectionView) }),
  ),

  // One connection's operations, with the connection alongside them.
  http.get(`${APIS_BASE}/:connection/operations`, ({ params }) => {
    const conn = findConnection(String(params.connection));
    if (!conn) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json({
      connection: connectionView(conn),
      operations: connectionOperations(conn),
    });
  }),

  // One operation in full.
  http.get(`${APIS_BASE}/:connection/operations/:operationId`, ({ params, request }) => {
    const conn = findConnection(String(params.connection));
    if (!conn) return new HttpResponse(null, { status: 404 });
    const spec = new URL(request.url).searchParams.get("spec") ?? undefined;
    const detail = connectionOperationDetail(conn, decodeId(params.operationId), spec);
    return detail ? HttpResponse.json(detail) : new HttpResponse(null, { status: 404 });
  }),

  // The operator's view: the operations one stored catalog spec parses to.
  http.get(`${ADMIN_BASE}/api-catalogs/:id/specs/:specName/operations`, ({ params }) => {
    const specName = String(params.specName);
    const spec = mockCatalogSpecs[String(params.id)]?.[specName];
    if (!spec) return new HttpResponse(null, { status: 404 });
    const basePath = specBasePath(spec.content ?? "{}", spec.base_path);
    return HttpResponse.json({
      operations: operationsFromSpec(spec.content ?? "{}", specName, basePath),
      base_path: basePath,
    });
  }),

  // One operation of one stored catalog spec.
  http.get(
    `${ADMIN_BASE}/api-catalogs/:id/specs/:specName/operations/:operationId`,
    ({ params }) => {
      const specName = String(params.specName);
      const spec = mockCatalogSpecs[String(params.id)]?.[specName];
      if (!spec) return new HttpResponse(null, { status: 404 });
      const detail = operationDetailFromSpec(
        spec.content ?? "{}",
        specName,
        specBasePath(spec.content ?? "{}", spec.base_path),
        decodeId(params.operationId),
      );
      return detail ? HttpResponse.json(detail) : new HttpResponse(null, { status: 404 });
    },
  ),
];
