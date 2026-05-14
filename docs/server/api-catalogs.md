# API Catalogs

OpenAPI specs describe **the API**, not the credential pointed at it. An organization with three Blackbaud Raiser's Edge NXT tenants has three connections — one per tenant's OAuth credentials — but they all talk to the same constituent, gift, and action endpoints. Pasting the same documentation into three connection records is duplication that drifts.

An **API catalog** is a versioned, globally-owned bundle of component OpenAPI 3.x specs. Each `(name, version)` pair is its own catalog row. Connections of kind `api` reference one catalog by id via `config.catalog_id`; the toolkit resolves connection → catalog → specs at runtime and exposes the merged operation index through `api_list_endpoints` and `api_get_endpoint_schema`.

## What's in a catalog

A catalog has:

- **id** — operator-chosen slug (lowercase alphanumeric + hyphens, 1–100 chars). Immutable after creation. Referenced by `connection_instances.config.catalog_id`.
- **name** — vendor/product slug, e.g. `blackbaud-renxt`. Free-text.
- **version** — optional free-text label, e.g. `2024-10`. Two catalogs may share a name but their `(name, version)` pair must be unique.
- **display_name** — operator-facing label in the portal.
- **description** — optional operator notes.
- A list of **component specs**, each with:
  - **spec_name** — slug surfaced to the model in `OperationSummary.spec` to disambiguate operations across components.
  - **content** — raw YAML or JSON OpenAPI 3.x document.
  - **source_kind** — `inline`, `upload`, or `url`.
  - **source_url / etag / last_fetched_at** — populated when `source_kind` is `url`.

Multiple connections can reference the same catalog. Editing a spec inside a catalog fans out to every referencing connection: the toolkit rebuilds each connection's parsed-doc state in place so `api_list_endpoints` and `api_get_endpoint_schema` reflect the new content without a process restart.

## Use cases

- **Blackbaud SKY** publishes Raiser's Edge NXT as a collection of resource-family specs (Constituent, Gift, Action, Address, etc.). Create one catalog `blackbaud-renxt-2024-10`, add each resource family as a component spec, and point every Blackbaud connection at it.
- **Salesforce REST** is a single large spec — one component, named `default`.
- **A bumpy upstream**: when the vendor releases an incompatible schema change, clone the catalog to a new version (`blackbaud-renxt-2025-01`) and update the spec content. Move connections to the new catalog when ready; the old catalog remains for connections that haven't migrated.

## Managing catalogs in the portal

The **API Catalogs** page lives in the admin sidebar alongside Connections. It shows every catalog grouped by `name` with each version listed underneath. Selecting a catalog opens an editor that lets you rename, update the display name and description, clone to a new version, or delete (delete is refused while any connection still references the catalog).

Inside the editor, the **Component specs** section lists each spec in the catalog. Click **Add spec** to open a modal with three tabs:

- **Paste** — paste YAML or JSON directly into the textarea. The server validates the content as OpenAPI 3.x before saving; a bad spec returns an error inline.
- **Upload** — pick a `.yaml`/`.yml`/`.json` file. Max 10 MB. Same validation step.
- **URL** — paste a public HTTPS URL. The server fetches once at save time, captures the ETag, and stores the content. Click **Refresh** on the spec row later to re-fetch.

URL-fetch enforces strict SSRF guards: HTTPS only, private/loopback/link-local/CGNAT IP ranges blocked (with a dial-time recheck to defeat DNS rebinding), 10 MB body cap, redirects refused. A public URL like `https://petstore3.swagger.io/api/v3/openapi.json` works; private-network URLs are rejected.

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
| `DELETE` | `/api/v1/admin/api-catalogs/{id}/specs/{spec}` | Delete one spec |

## Model-facing surface

From the model's perspective, catalogs are invisible. Three tools see them through the connection:

- `api_list_endpoints` returns one `OperationSummary` per operation across all component specs in the connection's catalog. Each summary carries a `spec` field set to the component spec name (e.g. `constituent`, `gift`) so the model can tell which spec defined the operation when names collide.
- `api_get_endpoint_schema` returns parameters, request body, and per-status response schemas for one operation. It strips `security`, `securitySchemes`, `servers`, and auth-vendor extensions (`x-amazon-*`, `x-google-*`, `x-azure-*`, `x-apigateway-*`) — the connection is pre-authenticated and the model has no business choosing auth. When an `operation_id` is defined by more than one component spec, the tool returns a structured error listing the candidates; the model retries with `spec` set.
- `api_invoke_endpoint` takes explicit `method` + `path`, so it doesn't need the spec qualifier — the catalog only feeds the discovery and schema-detail tools.

Per-call response size is capped at ~50 KB after marshal; deeper schemas truncate with a `note` field explaining the cap. The model can always fall back to `api_invoke_endpoint` to probe shape directly.
