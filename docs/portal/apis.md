---
description: Browsing the operations an API connection exposes - the operator's catalog view at /admin/apis, the caller's persona-scoped view at /apis, the read routes behind them, and the copyable gateway call each operation produces.
---

# API Operation Browser

The platform indexes the operations of every OpenAPI spec in an [API catalog](../server/api-catalogs.md), and an agent reaches them three ways: `api_list_endpoints` for a connection's operations, `api_get_endpoint_schema` for one operation's parameters and shapes, and relevance hits from `search`. The operation browser is the same index for a person.

There are two of them, and the difference is what they are drawn from.

## The operator's view

`/admin/apis` lists what has been **loaded**: every catalog, every component spec in it, and the operations each spec parses to — including catalogs no connection references yet. It is the answer to "what did we import", asked without opening the spec document.

![The operator's operation browser](../images/screenshots/light/admin-admin-apis-light.webp#only-light)![The operator's operation browser](../images/screenshots/dark/admin-admin-apis-dark.webp#only-dark)

A spec whose stored content no longer parses as OpenAPI is named above the index and its operations are absent, rather than the index quietly running short.

## The caller's view

`/apis` lists what this reader may **call**: the api-kind connections their persona allows, and on each of those, the operations the persona's `APIRoutes` rules permit. An operation a deny rule refuses is absent from the list and from the connection's count, which is the same subtraction `api_list_endpoints` applies, through the same route policy. The page and the tool cannot disagree about what a caller reaches.

The connection boundary is in force today. The per-route one is the same code path both surfaces already run through, but a persona's `APIRoutes` rules have no authoring surface yet — neither `platform.yaml` nor the admin API accepts them — so every persona currently carries an empty rule set and the route policy permits every operation on a connection the persona reaches. Authoring those rules is [#1479](https://github.com/txn2/mcp-data-platform/issues/1479).

![The caller's operation browser](../images/screenshots/light/user-apis-light.webp#only-light)![The caller's operation browser](../images/screenshots/dark/user-apis-dark.webp#only-dark)

An administrator's reach is unrestricted here as it is everywhere else: they see every connection and every operation.

## Reading an operation

The index is grouped by component spec and then by the tag the spec's author gave the operation, and filtered by free text over operation id, path and summary. Selecting a row opens it:

- the method, the full path including the spec's base-path prefix, and the operation id an `api_invoke_endpoint` call would name;
- parameters by location — path, query, header — each with its type, whether it is required, its description, and its enumeration when it has one;
- the request body: its media types, whether it is required, and the shape it takes;
- one entry per declared response status, with its media types, headers and body shape;
- on a connection, the requests [promoted from calls that worked](../portal/activity.md#my-calls) against that endpoint.

Every schema is shown as the shape a caller produces or receives, with the raw document one click away. It is the platform's own resolution — the same flattening `api_get_endpoint_schema` returns — not a second parse of the same document.

The selection is in the address bar, so one operation can be linked to:

```
/portal/apis?connection=acme-billing&spec=core&op=createCustomer
/portal/admin/apis?catalog=stripe-api-2025-01&spec=core&op=createCustomer
```

## The call an operation produces

On the caller's view, every operation carries the request that performs it through the [REST gateway](../server/api-gateway.md#rest-gateway-for-non-mcp-clients), copyable. The host in the command is the address you are reading the portal at, so it runs against your own deployment as copied; the capture below was taken against a local one.

![The gateway call an operation produces](../images/screenshots/light/user-apis-call-snippet-light.webp#only-light)![The gateway call an operation produces](../images/screenshots/dark/user-apis-call-snippet-dark.webp#only-dark)

The snippet is built from the operation itself: the method and path go into the invoke body, required query and header parameters appear with their types as placeholders, and the request body carries the smallest object the schema declares. A path parameter stays in the braces the spec wrote it in — that is the one part the caller has to replace, and a fabricated id would read as a working value.

The upstream call the gateway will make is stated above the snippet. The connection's own credential is what reaches the upstream; it is never part of this surface.

This is what an Apache NiFi `InvokeHTTP` processor, a cron job, or a shell needs, which is the second reason the browser exists: a client driving the platform over plain HTTP had no way to learn which connections exist or what they expose.

The page makes no upstream calls. It names operations and writes text.

## The read routes

Both views are read-only and neither returns a spec document.

| Route | Answers |
|-------|---------|
| `GET /api/v1/apis` | the api-kind connections the caller reaches, with the operation count they reach on each |
| `GET /api/v1/apis/{connection}/operations` | that connection's operations, with its upstream root and auth mode |
| `GET /api/v1/apis/{connection}/operations/{operationId}` | one operation's parameters, body and responses |
| `GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/operations` | the operations one stored catalog spec parses to |
| `GET /api/v1/admin/api-catalogs/{id}/specs/{spec}/operations/{operationId}` | one of those operations in full |

The `/api/v1/apis` routes are named outside `/api/v1/portal` because their second reader is not the portal, and they take any credential the portal takes: a session cookie, `Authorization: Bearer <token>`, or `X-API-Key: <key>`. They are mounted with the rest of the portal API, so they are present wherever it is — a deployment with a database, which is also the only kind that can hold a catalog. A caller whose roles map to no persona is refused; one whose persona reaches no api connection gets an empty list rather than a refusal.

An operation id with no declared `operationId` is synthesized as `METHOD path` (`GET /things/{id}`), so it carries a space and slashes. Percent-encode it as one path segment:

```bash
curl -sS -H "X-API-Key: $MCP_API_KEY" \
  "https://platform.example.com/api/v1/apis/acme-billing/operations/GET%20%2Fv1%2Fcustomers"
```

When one operation id is defined by more than one component spec of a catalog, pass `?spec=<name>`; without it the route answers `409` and names the specs to retry against.

## What it is not

The browser documents and produces snippets. It executes nothing: there is no "try it" here, and the invoke routes are the ones that make calls. It is also not where endpoint permissions are edited: it shows what the rules currently permit, and authoring them is [#1479](https://github.com/txn2/mcp-data-platform/issues/1479).
