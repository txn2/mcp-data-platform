import type {
  APIConnection,
  APIOperationDetail,
  APIOperationSummary,
  APIParameterDetail,
  APIResponseDetail,
} from "@/api/apis/types";
import type { APIRouteConnectionList } from "@/api/admin/types";
import { mockCatalogSpecs } from "./catalogs";

// Fixtures for the operation browser (#1478).
//
// The operations are not written out here: they are derived from the same
// OpenAPI documents the catalog fixtures already hold, by the same rules the
// platform applies (an operationId or a synthesized "METHOD path", the spec's
// base path prepended, parameters and bodies flattened). A second hand-written
// list would drift from the specs it claims to describe, and the drift would
// look like a working page.

/** METHODS is the verb set the platform walks a path item for. */
const METHODS = ["get", "post", "put", "patch", "delete", "head", "options"] as const;

interface RawOperation {
  summary?: string;
  description?: string;
  operationId?: string;
  tags?: string[];
  parameters?: APIParameterDetail[];
  requestBody?: {
    required?: boolean;
    description?: string;
    content?: Record<string, { schema?: unknown }>;
  };
  responses?: Record<string, { description?: string; content?: Record<string, { schema?: unknown }> }>;
}

interface RawSpec {
  servers?: { url?: string }[];
  paths?: Record<string, Record<string, RawOperation>>;
}

/** specBasePath is the prefix a spec's operations carry when it is read outside
 * a connection: the operator's override, or the path of its first server. */
export function specBasePath(content: string, override?: string): string {
  if (override) return override;
  const spec = JSON.parse(content) as RawSpec;
  const url = spec.servers?.[0]?.url;
  if (!url) return "";
  try {
    const path = new URL(url).pathname.replace(/\/$/, "");
    return path === "/" ? "" : path;
  } catch {
    return "";
  }
}

/** walkSpec visits every operation in a document, in (path, method) order. */
function walkSpec(
  content: string,
  visit: (method: string, path: string, op: RawOperation) => void,
): void {
  const spec = JSON.parse(content) as RawSpec;
  const paths = Object.entries(spec.paths ?? {}).sort(([a], [b]) => a.localeCompare(b));
  for (const [path, item] of paths) {
    for (const method of METHODS) {
      const op = item[method];
      if (op) visit(method.toUpperCase(), path, op);
    }
  }
}

/** operationsFromSpec is the index one spec parses to. */
export function operationsFromSpec(
  content: string,
  specName: string,
  basePath: string,
): APIOperationSummary[] {
  const out: APIOperationSummary[] = [];
  walkSpec(content, (method, path, op) => {
    out.push({
      operation_id: op.operationId || `${method} ${path}`,
      method,
      path: basePath + path,
      summary: op.summary,
      tags: op.tags,
      spec: specName,
    });
  });
  return out;
}

/** flattenSchema picks the one schema a content map carries, the way the
 * platform's flattener does for the common single-media-type operation. */
function flattenSchema(content?: Record<string, { schema?: unknown }>): {
  contentTypes: string[];
  schema: unknown;
} {
  const types = Object.keys(content ?? {});
  return { contentTypes: types, schema: types[0] ? content?.[types[0]]?.schema : undefined };
}

/** operationDetailFromSpec resolves one operation in full. */
export function operationDetailFromSpec(
  content: string,
  specName: string,
  basePath: string,
  operationID: string,
): APIOperationDetail | undefined {
  let found: APIOperationDetail | undefined;
  walkSpec(content, (method, path, op) => {
    const id = op.operationId || `${method} ${path}`;
    if (id !== operationID || found) return;
    const body = op.requestBody ? flattenSchema(op.requestBody.content) : undefined;
    const responses: APIResponseDetail[] = Object.entries(op.responses ?? {}).map(
      ([status, r]) => {
        const flat = flattenSchema(r.content);
        return {
          status,
          description: r.description,
          content_types: flat.contentTypes.length > 0 ? flat.contentTypes : undefined,
          schema: flat.schema,
        };
      },
    );
    found = {
      spec: specName,
      operation_id: id,
      method,
      path: basePath + path,
      summary: op.summary,
      description: op.description,
      parameters: op.parameters,
      request_body: op.requestBody
        ? {
            required: op.requestBody.required,
            description: op.requestBody.description,
            content_types: body?.contentTypes,
            schema: body?.schema,
          }
        : undefined,
      responses: responses.length > 0 ? responses : undefined,
    };
  });
  return found;
}

// ---------------------------------------------------------------------------
// The caller-scoped view: connections, each mounting one catalog.
// ---------------------------------------------------------------------------

/** MockAPIConnection ties a connection to the catalog whose specs describe it,
 * which is what the api-gateway toolkit does with catalog_id. */
interface MockAPIConnection {
  name: string;
  description: string;
  base_url: string;
  auth_mode: string;
  catalog_id: string;
  /** Operations this caller may not invoke, as "METHOD path" against the
   * resolved path. Stands in for the route policy's verdict, so the mocked
   * page shows the same subtraction a persona-scoped one does. The fixture
   * states the outcome rather than the rule because the caller whose persona
   * produced it is not modelled here; the persona editor's own fixture is the
   * unnarrowed index, which catalogOperations returns. */
  denied?: string[];
}

export const mockAPIConnections: MockAPIConnection[] = [
  {
    name: "acme-billing",
    description: "ACME billing and subscriptions",
    base_url: "https://api.stripe.com",
    auth_mode: "bearer",
    catalog_id: "stripe-api-2025-01",
    // A read-only persona: the writes are refused, so they are absent here.
    denied: ["POST /v1/charges", "POST /v1/payment_intents"],
  },
  {
    name: "acme-crm",
    description: "ACME revenue operations CRM",
    base_url: "https://acme.my.salesforce.com",
    auth_mode: "oauth2_client_credentials",
    catalog_id: "salesforce-rest-2025-01",
    denied: ["DELETE /services/data/v59.0/sobjects/{sobject}/{id}"],
  },
];

/** connectionOperations is one connection's whole index: every spec of its
 * catalog, minus what the route policy denies. */
export function connectionOperations(conn: MockAPIConnection): APIOperationSummary[] {
  const specs = mockCatalogSpecs[conn.catalog_id] ?? {};
  const denied = new Set(conn.denied ?? []);
  const out: APIOperationSummary[] = [];
  for (const [specName, spec] of Object.entries(specs)) {
    const basePath = specBasePath(spec.content ?? "{}", spec.base_path);
    for (const op of operationsFromSpec(spec.content ?? "{}", specName, basePath)) {
      if (!denied.has(`${op.method} ${op.path}`)) out.push(op);
    }
  }
  return out.sort(
    (a, b) =>
      (a.spec ?? "").localeCompare(b.spec ?? "") ||
      a.path.localeCompare(b.path) ||
      a.method.localeCompare(b.method),
  );
}

/** connectionView is the wire shape of one connection, with the counts the
 * caller reaches rather than the catalog's totals. */
export function connectionView(conn: MockAPIConnection): APIConnection {
  const operations = connectionOperations(conn);
  const specs = mockCatalogSpecs[conn.catalog_id] ?? {};
  return {
    name: conn.name,
    description: conn.description,
    base_url: conn.base_url,
    auth_mode: conn.auth_mode,
    catalog_id: conn.catalog_id,
    operation_count: operations.length,
    specs: Object.entries(specs).map(([specName, spec]) => ({
      name: specName,
      title: spec.title,
      description: spec.description,
      operation_count: operations.filter((op) => op.spec === specName).length,
      base_path: specBasePath(spec.content ?? "{}", spec.base_path),
    })),
  };
}

/** connectionOperationDetail resolves one operation of one connection, or
 * undefined when the id is unknown or the route policy denies it. */
export function connectionOperationDetail(
  conn: MockAPIConnection,
  operationID: string,
  specFilter?: string,
): APIOperationDetail | undefined {
  const visible = connectionOperations(conn).find((op) => op.operation_id === operationID);
  if (!visible) return undefined;
  const specName = specFilter || visible.spec || "";
  const spec = mockCatalogSpecs[conn.catalog_id]?.[specName];
  if (!spec) return undefined;
  return operationDetailFromSpec(
    spec.content ?? "{}",
    specName,
    specBasePath(spec.content ?? "{}", spec.base_path),
    operationID,
  );
}

/** catalogOperations is one connection's whole index, narrowed by nothing.
 *
 * It is what the persona editor writes rules against: an operator writing rules
 * for one persona is not that persona, so the authoring surface shows what
 * exists rather than what the reader reaches (#1479). */
export function catalogOperations(conn: MockAPIConnection): APIOperationSummary[] {
  const specs = mockCatalogSpecs[conn.catalog_id] ?? {};
  const out: APIOperationSummary[] = [];
  for (const [specName, spec] of Object.entries(specs)) {
    const basePath = specBasePath(spec.content ?? "{}", spec.base_path);
    out.push(...operationsFromSpec(spec.content ?? "{}", specName, basePath));
  }
  return out.sort(
    (a, b) =>
      (a.spec ?? "").localeCompare(b.spec ?? "") ||
      a.path.localeCompare(b.path) ||
      a.method.localeCompare(b.method),
  );
}

/** mockAPIRouteConnections is the payload of the persona editor's inventory
 * route: every api-kind connection with every operation its catalog declares. */
export function mockAPIRouteConnections(): APIRouteConnectionList {
  const connections = mockAPIConnections.map((conn) => ({
    name: conn.name,
    description: conn.description,
    base_url: conn.base_url,
    auth_mode: conn.auth_mode,
    catalog_id: conn.catalog_id,
    operations: catalogOperations(conn),
  }));
  return { connections, total: connections.length };
}
