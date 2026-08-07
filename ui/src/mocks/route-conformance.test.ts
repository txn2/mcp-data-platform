/**
 * MSW Route Conformance Tests (issue #896)
 *
 * `conformance.test.ts` validates that mock DATA shapes match the generated
 * types. This file validates that mock ROUTES (path + method) actually exist in
 * the backend's OpenAPI surface (`internal/apidocs/swagger.json`). Without this,
 * a Go route rename leaves the UI e2e suite green while every mocked call hits a
 * phantom endpoint that no longer exists.
 *
 * Direction: we only assert mocks-are-in-the-spec. The reverse (spec routes with
 * no mock) is legitimate partial mocking and is NOT asserted.
 *
 * The swagger spec only documents the routes registered on the annotated admin
 * mux. Several real backend routes are served on separate muxes that carry no
 * swagger annotations (the portal DataHub proxy, the admin public mux, the
 * observability PromQL proxy). Those are enumerated in ALLOWLIST below with the
 * Go source that proves they exist, so this test cannot be satisfied by adding a
 * route to the allowlist without a real handler behind it.
 */
import { readFileSync } from "fs";
import path from "path";
import { fileURLToPath } from "url";
import { describe, it, expect } from "vitest";
import { handlers } from "./handlers";

// swagger.json lives at the repo root, one level above the ui/ workspace. Read
// it with Node fs (not a module import) so vite's fs.allow boundary does not
// apply, and resolve relative to this file rather than the cwd.
const dir = path.dirname(fileURLToPath(import.meta.url));
const specPath = path.resolve(dir, "../../../internal/apidocs/swagger.json");
const spec = JSON.parse(readFileSync(specPath, "utf-8")) as {
  basePath?: string;
  paths: Record<string, Record<string, unknown>>;
};
const basePath = spec.basePath ?? "";

// Canonical form: params collapsed to {} so MSW's `:conn` and swagger's `{conn}`
// compare equal, trailing slash removed. Applied to both sides.
function normalize(p: string): string {
  return p
    .replace(/\{[^}]+\}/g, "{}")
    .replace(/:[A-Za-z0-9_]+/g, "{}")
    .replace(/\/$/, "");
}

function key(method: string, normalizedPath: string): string {
  return `${method.toUpperCase()} ${normalizedPath}`;
}

// Real backend routes served outside the swagger-annotated admin mux. Each is
// proven by the cited Go source; the test rejects an allowlist entry that no
// mock actually emits, so this list cannot silently rot.
const ALLOWLIST = new Set<string>([
  // internal/httpserver/datahubapi/handler.go Register (portal DataHub proxy mux).
  "GET /portal/datahub/connections",
  "GET /portal/datahub/{}/catalog/browse",
  "GET /portal/datahub/{}/catalog/search",
  "GET /portal/datahub/{}/catalog/entity",
  "GET /portal/datahub/{}/catalog/lookup/tags",
  "GET /portal/datahub/{}/catalog/lookup/glossary-terms",
  "GET /portal/datahub/{}/catalog/lookup/domains",
  "POST /portal/datahub/{}/catalog/tags",
  "DELETE /portal/datahub/{}/catalog/tags",
  "POST /portal/datahub/{}/catalog/domains",
  "DELETE /portal/datahub/{}/catalog/domains",
  // internal/httpserver/datahubapi/glossary.go glossaryRoutes.
  "GET /portal/datahub/{}/catalog/glossary/roots",
  "GET /portal/datahub/{}/catalog/glossary/children",
  "GET /portal/datahub/{}/catalog/glossary/parents",
  "GET /portal/datahub/{}/catalog/glossary/term",
  "POST /portal/datahub/{}/catalog/glossary/nodes",
  "POST /portal/datahub/{}/catalog/glossary/terms",
  "DELETE /portal/datahub/{}/catalog/glossary/entity",
  "GET /portal/datahub/{}/catalog/entity/documents",
  "PUT /portal/datahub/{}/catalog/entity/description",
  "PUT /portal/datahub/{}/catalog/entity/tags",
  "PUT /portal/datahub/{}/catalog/entity/owners",
  "PUT /portal/datahub/{}/catalog/entity/glossary-terms",
  "PUT /portal/datahub/{}/catalog/entity/domain",
  "GET /portal/datahub/{}/documents/browse",
  "GET /portal/datahub/{}/documents/search",
  "GET /portal/datahub/{}/documents/{}",
  "POST /portal/datahub/{}/documents",
  "PUT /portal/datahub/{}/documents/{}",
  "DELETE /portal/datahub/{}/documents/{}",
  // pkg/admin/handler.go: public (unauthenticated) branding endpoint on publicMux.
  "GET /admin/public/branding",
  // pkg/observability/proxy/handler.go: authenticated PromQL proxy mux.
  "GET /observability/query",
  "GET /observability/query_range",
]);

// Spec routes, keyed by method + normalized path (basePath included in the key).
const specRoutes = new Set<string>();
for (const [p, item] of Object.entries(spec.paths)) {
  for (const method of Object.keys(item)) {
    specRoutes.add(key(method, normalize(p)));
  }
}

// MSW mocked routes with a string path (RegExp/predicate handlers, if any, are
// not path-comparable and are skipped).
interface MockRoute {
  method: string;
  fullPath: string;
  // Canonical METHOD + normalized-path key, used for both the spec lookup and
  // the allowlist lookup (the spec paths and the allowlist share this form).
  key: string;
}
const mockRoutes: MockRoute[] = [];
for (const h of handlers) {
  const info = (h as { info?: { method?: unknown; path?: unknown } }).info;
  if (!info || typeof info.path !== "string" || typeof info.method !== "string") continue;
  const method = info.method;
  const fullPath = info.path;
  const specPathPart =
    basePath && fullPath.startsWith(basePath) ? fullPath.slice(basePath.length) : fullPath;
  mockRoutes.push({ method, fullPath, key: key(method, normalize(specPathPart)) });
}

describe("MSW route conformance", () => {
  it("has string-path mock handlers to check", () => {
    expect(mockRoutes.length).toBeGreaterThan(0);
  });

  it("every mocked route exists in the OpenAPI spec or the justified allowlist", () => {
    const drift = mockRoutes
      .filter((r) => !specRoutes.has(r.key) && !ALLOWLIST.has(r.key))
      .map((r) => `${r.method} ${r.fullPath}`)
      .sort();
    expect(
      drift,
      `Mocked routes absent from swagger.json (${specPath}) and not allowlisted. ` +
        `A Go route was likely renamed/removed, or a new unspecced route needs a ` +
        `justified ALLOWLIST entry:\n${drift.join("\n")}`,
    ).toEqual([]);
  });

  it("has no stale allowlist entries (every allowlisted route is still mocked)", () => {
    const mockedAllowKeys = new Set(mockRoutes.map((r) => r.key));
    const stale = [...ALLOWLIST].filter((k) => !mockedAllowKeys.has(k)).sort();
    expect(
      stale,
      `Allowlisted routes no longer emitted by any mock handler; remove them:\n${stale.join("\n")}`,
    ).toEqual([]);
  });
});
