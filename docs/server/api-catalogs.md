# API Catalogs

OpenAPI specs describe **the API**, not the credential pointed at it. An organization with a Salesforce sandbox and a Salesforce production org has two connections, one set of credentials each, but both talk to the same sObjects, query, and bulk-job endpoints. Pasting the same documentation into both connection records is duplication that drifts.

An **API catalog** is a versioned, globally-owned bundle of component OpenAPI 3.x specs. Each `(name, version)` pair is its own catalog row. Connections of kind `api` reference one catalog by id via `config.catalog_id`; the toolkit resolves connection, catalog, and specs at runtime and exposes the merged operation index through `api_list_endpoints` and `api_get_endpoint_schema`.

## What's in a catalog

A catalog has:

- **id**: operator-chosen slug (lowercase alphanumeric + hyphens, 1 to 100 chars). Immutable after creation. Referenced by `connection_instances.config.catalog_id`.
- **name**: vendor/product slug, e.g. `salesforce-rest`. Free-text.
- **version**: optional free-text label, e.g. `2024-10`. Two catalogs may share a name but their `(name, version)` pair must be unique.
- **display_name**: operator-facing label in the portal.
- **description**: optional operator notes.
- A list of **component specs**, each with:
  - **spec_name**: slug surfaced to the model in `OperationSummary.spec` to disambiguate operations across components.
  - **content**: raw YAML or JSON OpenAPI 3.x document.
  - **source_kind**: `inline`, `upload`, or `url`.
  - **source_url / etag / last_fetched_at**: populated when `source_kind` is `url`.

Multiple connections can reference the same catalog. Editing a spec inside a catalog fans out to every referencing connection: the toolkit rebuilds each connection's parsed-doc state in place so `api_list_endpoints` and `api_get_endpoint_schema` reflect the new content without a process restart.

## Use cases

- **Google Workspace** publishes Drive, Calendar, Gmail, and Admin SDK as separate API specs. Create one catalog `google-workspace-2024-10`, add each as a component spec (`drive`, `calendar`, `gmail`, `admin`), and point every Google Workspace connection at it.
- **Salesforce REST** is a single large spec, one component named `default`. The dev sandbox connection and the production connection both reference the same catalog.
- **A bumpy upstream**: when the vendor releases an incompatible schema change, clone the catalog to a new version (`salesforce-rest-2025-01`) and update the spec content. Move connections to the new catalog when ready; the old catalog remains for connections that haven't migrated.

## Managing catalogs in the portal

The **API Catalogs** page lives in the admin sidebar alongside Connections. It shows every catalog grouped by `name` with each version listed underneath. Selecting a catalog opens an editor that lets you rename, update the display name and description, clone to a new version, or delete (delete is refused while any connection still references the catalog).

Inside the editor, the **Component specs** section lists each spec in the catalog. Click **Add spec** to open a modal with three tabs:

- **Paste** — paste YAML or JSON directly into the textarea. The server validates the content as OpenAPI 3.x before saving; a bad spec returns an error inline.
- **Upload** — pick a `.yaml`/`.yml`/`.json` file. Max 10 MB. Same validation step.
- **URL** — paste a public HTTPS URL. The server fetches once at save time, captures the ETag, and stores the content. Click **Refresh** on the spec row later to re-fetch.

URL-fetch enforces strict SSRF guards: HTTPS only, private/loopback/link-local/CGNAT IP ranges blocked (with a dial-time recheck to defeat DNS rebinding), 10 MB body cap, redirects refused. A public URL like `https://petstore3.swagger.io/api/v3/openapi.json` works; private-network URLs are rejected.

### What "validates as OpenAPI 3.x" means

Structural validation runs in full: the document must be parseable JSON or YAML, `openapi` must be `3.x`, `info.title`/`version` must be present, operation IDs must be unique within a spec, `$ref` targets must resolve inside the document, and parameter / request body / response shapes must be well-formed. Vendor specs that fail any of these are rejected with an inline message pointing at the offending path.

Three categories of strict-validation checks are intentionally relaxed, matching what production OpenAPI consumers (Swagger UI, Postman, Insomnia) accept:

1. **Example vs schema drift.** A property declared `type: object` may carry string examples like `"Blue"` or an ISO timestamp. Examples are documentation hints, not part of the wire contract.
2. **Regex patterns Go does not support.** Specs that use ECMA constructs like lookahead (`(?=...)`) parse instead of failing.
3. **Default-value drift.** Same documentation-only role as examples.

External `$ref` resolution stays disabled at the parser regardless of source (paste / upload / URL), so a malicious spec cannot trigger an SSRF at parse time by referencing a private-network URL.

## Wiring a connection to a catalog

Open a `kind: api` connection in the Connections page. The OpenAPI Catalog dropdown lists every catalog known to the platform; pick one and save. The model immediately sees the catalog's operations the next time it calls `api_list_endpoints` against that connection.

A connection with no catalog selected (or an empty `catalog_id`) still works — the model can call `api_invoke_endpoint` with an explicit method and path. It just won't have discovery via `api_list_endpoints` or schema retrieval via `api_get_endpoint_schema`.

The legacy `openapi_spec` JSONB key (inline-only, per-connection) is no longer read by the toolkit. Connections that still carry it surface a banner in the editor prompting the operator to move the content into a catalog.

## REST API

The admin REST API matches the portal one-to-one. All routes require admin auth.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/admin/api-catalogs` | List catalogs (with spec_count and ref_count) |
| `POST` | `/api/v1/admin/api-catalogs` | Create catalog header |
| `GET` | `/api/v1/admin/api-catalogs/{id}` | Catalog detail |
| `PUT` | `/api/v1/admin/api-catalogs/{id}` | Partial update (name, version, display_name, description) |
| `DELETE` | `/api/v1/admin/api-catalogs/{id}` | Delete catalog (refused if referenced by any connection) |
| `POST` | `/api/v1/admin/api-catalogs/{id}/clone` | Clone catalog and all specs to a new id/version |
| `GET` | `/api/v1/admin/api-catalogs/{id}/specs` | List component specs (metadata only) |
| `GET` | `/api/v1/admin/api-catalogs/{id}/specs/{spec}` | Get one spec with content |
| `PUT` | `/api/v1/admin/api-catalogs/{id}/specs/{spec}` | Upsert spec (inline or URL source) |
| `PUT` | `/api/v1/admin/api-catalogs/{id}/specs/{spec}/upload` | Multipart upload of a spec file |
| `POST` | `/api/v1/admin/api-catalogs/{id}/specs/{spec}/refresh` | Re-fetch a URL-sourced spec |
| `POST` | `/api/v1/admin/api-catalogs/{id}/specs/{spec}/reembed` | Recompute and persist per-operation embeddings for the spec |
| `DELETE` | `/api/v1/admin/api-catalogs/{id}/specs/{spec}` | Delete one spec |

## Persisted operation embeddings

Semantic and hybrid ranking on `api_list_endpoints` need a vector per operation. The toolkit stores these in PostgreSQL (`api_catalog_operation_embeddings`, migration 000044) keyed on `(catalog_id, spec_name, operation_id)` with a 768-dimensional `pgvector` column. Two consequences:

1. **Computed once per spec write, not per connection.** The admin spec-upsert path runs the configured embedding provider, hashes the source text, and writes one row per operation. The toolkit reads those rows when a connection registers and stays out of the embedding-provider's way at request time.
2. **Survives restart.** A redeployed pod re-reads the same rows; `api_list_endpoints(ranking=semantic)` returns ranked results on the first call without a warm-up window.

Each row records the SHA-256 of the source text, so a spec refresh that reuses operation text skips the embed call for unchanged rows. Spec deletion drops vector rows via `ON DELETE CASCADE`.

When the embedding provider is not configured at spec-upsert time, the row count stays at 0 and the toolkit falls back to lexical ranking with `errEmbeddingsNotIndexed` in the response Note. The portal's catalog editor surfaces this state ("embeddings: N indexed" vs "not indexed") with a Re-embed button that calls the admin endpoint above.

## Model-facing surface

From the model's perspective, catalogs are invisible. Three tools see them through the connection:

- `api_list_endpoints` returns one `OperationSummary` per operation across all component specs in the connection's catalog. Each summary carries a `spec` field set to the component spec name (e.g. `constituent`, `gift`) so the model can tell which spec defined the operation when names collide.
- `api_get_endpoint_schema` returns parameters, request body, and per-status response schemas for one operation. It strips `security`, `securitySchemes`, `servers`, and auth-vendor extensions (`x-amazon-*`, `x-google-*`, `x-azure-*`, `x-apigateway-*`) — the connection is pre-authenticated and the model has no business choosing auth. When an `operation_id` is defined by more than one component spec, the tool returns a structured error listing the candidates; the model retries with `spec` set.
- `api_invoke_endpoint` takes explicit `method` + `path`, so it doesn't need the spec qualifier — the catalog only feeds the discovery and schema-detail tools.

Per-call response size is capped at ~50 KB after marshal; deeper schemas truncate with a `note` field explaining the cap. The model can always fall back to `api_invoke_endpoint` to probe shape directly.
