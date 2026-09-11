# GraphQL Toolkit

The GraphQL toolkit (`kind: graphql`) reaches a GraphQL endpoint through the platform's auth, persona, audit and export pipeline. It is the third gateway kind, alongside the MCP [Gateway Toolkit](gateway.md) and the [API Gateway Toolkit](api-gateway.md).

It is a kind of its own rather than a shape of API connection because a GraphQL endpoint is one URL that everything is POSTed to. There is no route to authorize, no path to rank, and a failure arrives as an `errors` array inside an HTTP 200. What the kind adds on top of the shared upstream transport is a schema it keeps by introspection, an index of the operations that schema exposes, validation of the document before it is sent, and an operation-level policy that reduces a document to the fields it actually selects.

Three tools serve every connection: `graphql_discover` finds an operation and renders a document that runs it, `graphql_query` executes one, and `graphql_export` streams one into a portal asset. No tool is generated per operation, so a schema with a thousand fields does not inflate the tool catalog.

## Registering a connection

Register the upstream as a connection of kind `graphql`, in the portal under **Admin > Connections** or through `PUT /api/v1/admin/connection-instances/graphql/{name}`. In YAML:

```yaml
toolkits:
  graphql:
    enabled: true
    instances:
      metadata:
        endpoint_url: "https://datahub.example.com/api/graphql"
        auth_mode: bearer
        credential: "${DATAHUB_TOKEN}"
        description: "The metadata service's GraphQL API."
        schema_validation: strict
        max_query_depth: 15
        namespace_depth: 3
        read_only: false
```

| Key | Meaning |
| --- | --- |
| `endpoint_url` | The full URL documents are POSTed to. Required. Unlike an HTTP API's `base_url` this is the whole address: a GraphQL endpoint has exactly one |
| `description` | Human-readable description, surfaced by `list_connections` and the admin UI. Empty falls back to the endpoint |
| `auth_mode` and its credentials | The shared upstream authentication modes: `none`, `bearer`, `api_key`, `basic`, `signed_jwt`, `oauth`, `mtls`. Same keys, same behavior and same at-rest encryption as the API gateway's. `signed_jwt` is what a Sage X3 connected application needs; see [Signed JWT upstreams](signed-jwt-auth.md) |
| `static_headers` | Headers attached to every outbound request. This is where an upstream's tenant or folder routing goes, and where a `User-Agent` other than the platform's default `mcp-data-platform/<version>` is pinned. Operator-owned; the model never sets or overrides them |
| `connect_timeout`, `call_timeout` | Dial and per-call bounds. Default 10s and 60s |
| `max_response_bytes` | Upstream read cap: the most the platform reads of one response. Default 10 MiB |
| `max_inline_bytes` | Model-context budget: the most a rendered `graphql_query` result may hold. Default 32 KiB |
| `mtls_client_cert_pem`, `mtls_client_key_pem`, `tls_ca_bundle_pem` | The connection's TLS material, as on an `api` connection |
| `identity_passthrough` | Forwards the acting caller's inbound bearer token instead of this connection's credential |
| `schema_validation` | `strict` (default) or `warn`. See [Schema validation](#schema-validation) |
| `max_query_depth` | Deepest selection a document may have. Default 15 |
| `namespace_depth` | How many segments a dotted operation id may have. Default 3. See [Namespace descent](#namespace-descent) |
| `read_only` | Refuses every mutation document on this connection, for every persona |

The credential, header, timeout, response-cap and TLS keys are read by `internal/upstreamauth`, the same code the API gateway reads them with, so an operator who has configured one kind has configured the other.

## The schema

The platform keeps each connection's schema and serves discovery from it rather than from the endpoint.

**On register and on reconcile** the toolkit runs the canonical introspection query through the connection's own credential, converts the result to schema definition language, and stores it compressed with its SHA-256 hash and the time it was read. The hash is what the operation index and its embeddings are keyed on: a re-read that produces the same schema leaves both alone.

**Admin > Connections** shows what the platform holds for a graphql connection — how many operations, whether it was introspected or uploaded, when, and the hash — with a button to re-read it from the endpoint. `GET /api/v1/admin/connection-instances/graphql/{name}/schema` reports the same state, and `POST .../refresh-schema` performs the read.

**An endpoint that disables introspection** is a named error on the connection, not a silent empty index: `graphql_discover` refuses and quotes what the upstream said. For that case an operator supplies the schema themselves — paste it into the same panel, or POST it as the body of `refresh-schema`. Both SDL and a saved introspection result are accepted, in either the full GraphQL response shape or the `__schema` object alone.

**An endpoint behind a web application firewall** may answer the introspection with HTTP 403 and an HTML block page, which says nothing about the request. The platform sends every request as `User-Agent: mcp-data-platform/<version>` rather than Go's default, which is the value such a rule most often refuses; when a 403 HTML page comes back anyway, the error recorded on the connection names the User-Agent the request carried and the key that changes it, `static_headers: {"User-Agent": "<value>"}`. The pinned value is then what every query and every page of a walk on that connection presents.

The introspection query the platform sends is at the compatibility level every GraphQL server implements: no `isRepeatable` on directives, no `specifiedByURL` on scalars, no `includeDeprecated` argument on `args`. The cost of that floor is that directive repeatability is not recorded, so a document repeating a custom directive on one element is refused under strict validation; such a document passes under `warn`.

### Namespace descent

An operation is a field on the query or mutation root, or a field reached by descending through fields that are namespaces rather than work.

The walk descends through a field when it takes no arguments and returns a singular object type that is not a Relay connection. Everything else is terminal and becomes an operation, with one exception: a field that takes no arguments and yields only scalar data is a column of its parent's record rather than a unit of work, so the object holding it is emitted instead. `namespace_depth` caps how many segments a dotted id may have.

A flat schema therefore indexes its root fields:

```
query:searchAcrossEntities
query:dataset
mutation:updateDescription
```

A namespaced one — a package, an entity, then a verb — indexes the verbs:

```
query:masterData.product.query
query:masterData.product.read
mutation:sales.salesOrder.create
```

A Relay connection is recognized by its shape (an `edges` or a `pageInfo` field) rather than by its name, so a schema that names an entity's namespace `<Entity>_Connection` while giving it verb fields is descended into, and one that returns real `edges` is where the walk stops.

## Discovering an operation

`graphql_discover` takes a `connection` and answers at one of two depths. Every response carries `level`, `schema_hash` and `schema_fetched_at`, so a caller comparing two answers can see whether the schema moved underneath them.

- **Operations** (`level: operations`): a call with or without `query` returns the matching operations (`operation_id`, `kind`, `path`, `summary`, `return_type`, `arguments`). `kind` is `QUERY` or `MUTATION` and `path` is the dotted id with dots as slashes — the two coordinates a persona rule names.

  A ranked row carries `score` and `lexical_match`, and the result carries `matched_lexical` and `shown_semantic`: how many operations contain every token of the query, and how many followed them as neighbors by intent. Ranking is `hybrid` by default once the connection's schema has been indexed, `lexical` otherwise, and a mode that fell back says why in `note`. The relevance arithmetic is the platform's own, shared with the API gateway.

- **Operation** (`level: operation`): a call with `operation_id` returns that operation's arguments with their types, descriptions and defaults; the input-object types those arguments reference, expanded; the return type's field tree; and a **skeleton document with a variables stub**.

The skeleton is the load-bearing part. It already carries the selection set the operation needs, including the `edges { node { ... } }` shape of a paged connection, and it validates against the connection's stored schema, so the first `graphql_query` call is a real call rather than a round of validation errors:

```graphql
query masterDataProductQuery($first: Int, $after: String, $filter: String) {
  masterData {
    product {
      query(first: $first, after: $after, filter: $filter) {
        edges {
          node {
            _id
            name
          }
        }
        pageInfo {
          hasNextPage
          endCursor
        }
      }
    }
  }
}
```

Every argument is declared as a variable, and the stub carries the required ones. A variable the document declares but the stub omits is absent from the call, which is what leaves the schema's own default in force; a caller adds an optional argument by adding its key.

Operations the persona's route rules deny are absent from the list and reported as not found at the operation level.

## Running a document

`graphql_query` takes the `connection`, the `query` document, its `variables`, and `operation_name` when the document defines more than one operation.

`variables` accepts a JSON object (`{"first": 10}`) or a string holding that object's JSON (`"{\"first\": 10}"`). Both forms reach the same request: a client that stringifies structured arguments sends the second, and refusing it would make the tool work for some clients and not others.

Before anything is sent, the document is parsed, the operation to run is resolved, and it is checked. Refused without a call: a subscription; a `__schema` or `__type` selection (the platform serves the schema through `graphql_discover`); a document deeper than `max_query_depth`; a multi-operation document with no `operation_name`; a document the persona's rules deny; and, under strict validation, one the stored schema does not admit.

### Schema validation

`schema_validation: strict` (the default) refuses a document naming a field the connection's schema does not have, and the refusal names the connection and when its schema was read — which is what makes schema drift diagnose itself rather than present as an upstream error. `warn` sends the document anyway and reports the violations in `validation_warnings` alongside the answer, for a deployment whose endpoint has moved ahead of the stored schema.

### Failures arrive in the body

A GraphQL endpoint reports almost every failure as an HTTP 200 carrying an `errors` array. The result reports `upstream_error: true` for a non-2xx *or* a 200 with errors, and that is what the platform's audit, call catalog and outbound metrics classify the call on. The `errors` array is passed through unchanged — its messages, paths and `extensions` are the upstream's own words and the only diagnosis a caller gets — and any partial `data` is preserved beside it.

### Results too large to read

`max_inline_bytes` bounds the rendered result. A result past it has its `data` withheld — a JSON document cut in half cannot be parsed, so it is withheld whole rather than halved — sets `data_truncated`, reports `data_bytes`, and carries `export_arguments`: the `graphql_export` call that writes the same result to a portal asset.

### Paging

Pass `paginate` to walk a paged connection inside one call and receive one page's shape holding every row.

```json
{
  "connection": "erp",
  "query": "query P($after: String) { masterData { product { query(after: $after) { edges { node { _id name } } pageInfo { hasNextPage endCursor } } } } }",
  "paginate": {
    "items": "masterData.product.query.edges",
    "cursor_variable": "after",
    "max_pages": 5
  }
}
```

`items` names the array to merge, as a dotted path inside the response's `data`. `cursor_variable` names the document variable each page's cursor is bound to, so the document itself is unchanged between pages. By default the cursor is read from the Relay `pageInfo` beside the items array (`pageInfo.endCursor`, `pageInfo.hasNextPage`).

An upstream that pages some other way names its own path. A scroll-style API with a bare next cursor and no `pageInfo`:

```json
{
  "paginate": {
    "items": "searchAcrossEntities.searchResults",
    "cursor_variable": "scrollId",
    "next_cursor_path": "searchAcrossEntities.nextScrollId"
  }
}
```

The walk stops when the upstream reports no next page, when a cursor comes back null or absent, at `max_pages` (default 10), or on the first page the upstream refuses. The result's `pagination` reports `pages_fetched`, `items_merged`, `stopped_by` (`end`, `max_pages`, `error`) and, when it stopped at the page bound, the `next_cursor` the walk would have used.

## Exporting a result

`graphql_export` runs the same document under the same gates and writes the result into a portal asset instead of returning it through the model context. It takes `graphql_query`'s arguments plus the asset metadata (`name`, `description`, `tags`, `idempotency_key`, `create_public_link`) and returns the asset's id, URL, size and share link — not the data. `paginate` works there too: the merged pages go into the one asset.

The export is all or nothing: a result past the platform's `portal.export.max_bytes` cap is refused rather than written partly, because a partial asset reads as a complete answer to whoever opens it later.

## Authorizing an operation

A GraphQL operation carries the same three coordinates an HTTP endpoint does, which is what lets a persona's existing `api_routes` rules govern it: the **method** is the operation kind (`QUERY` or `MUTATION`) and the **path** is the dotted operation id with dots as slashes.

A read-only persona on a GraphQL connection is one rule — an **allow** naming the QUERY method:

```yaml
personas:
  analyst:
    connections:
      allow: ["*"]
    api_routes:
      - connection: "erp"
        methods: ["QUERY"]
```

Naming no `paths` is what makes it cover every operation: a path glob would not, because a glob's `*` does not cross a `/` and `**` is not recursive. And an allow is what expresses it, because `api_routes` is a narrowing: once any rule names a connection, a call needs a matching allow, so a mutation is refused by matching none. A deny beside it states the intent and is what the refusal names, but a deny on its own would close the connection entirely rather than make it read-only.

Name paths to scope further, one rule per depth:

```yaml
    api_routes:
      - connection: "erp"
        methods: ["QUERY"]
        paths: ["/masterData/product/*"]
```

The document is reduced to the operations it invokes, not to its root fields: `{ masterData { product { read(_id: "1") { _id } } } }` is authorized as `QUERY /masterData/product/read`, so a rule can name an entity's verb rather than only a whole package. Aliases are resolved to the underlying field name and fragments are expanded, so a denied operation cannot be reached by renaming it or hiding it in a fragment. Every operation a document invokes must pass: a mixed document is refused when any one of them is denied, rather than being sent with the denied part stripped.

`read_only: true` on the connection refuses mutations for every persona, which is what an operator sets once when an endpoint is mounted for reporting.

## Search and the call catalog

A GraphQL operation joins the universal `search` tool's **endpoints** group alongside the API gateway's operations. Both answer one question — which remote operation serves this intent — and a caller narrowing a search to `endpoints` does not have to know which kind their connection is. The per-connection route policy is applied first, so a federated search never surfaces an operation a scoped `graphql_discover` would have hidden.

Semantic ranking needs the connection's schema to be indexed. The platform's index-jobs framework embeds each connection's operations off the request path under the source kind `graphql_operations`, keyed on `(connection, schema_hash, operation_id)`: a schema change leaves the previous version's vectors addressable, and a pass that has not run yet leaves ranking lexical, with the note saying so.

A `graphql_query` or `graphql_export` call is recorded in the [call catalog](configuration.md#call-catalog-configuration) as a `graphql` record: the document is its statement, `QUERY`/`MUTATION` is its method (which is what says whether the call only read), and the fields the document selected are its targets. On a saved asset's provenance panel a GraphQL call renders as its document rather than as a request line, because every GraphQL call is a POST to one URL.

## What is not here

- **Subscriptions.** Refused with a clear message: a subscription is a long-lived stream over a transport this kind does not open.
- **A client-minted JWT auth mode.** An upstream that expects a short-lived token the client signs itself is not reachable through any of the modes above; that is a separate change on the shared upstream-auth seam.
- **Cursor-following bulk export.** `graphql_export` walks pages exactly as `graphql_query` does, bounded by `max_pages`; there is no unbounded streaming export.
