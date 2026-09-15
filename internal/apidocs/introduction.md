The platform's REST surface: the admin API, the portal API, and the gateway
routes a non-MCP client calls an upstream connection through.

Every route below authenticates. Almost every one answers JSON; the handful
that return a file, an image, a page or YAML say so in their `produces`.

# Getting started

The base URL is the origin you fetched this document from, followed by
`/api/v1`. The `host` at the top of this reference is that origin: the
document is rewritten as it is served, so the URLs here are the ones this
deployment answers on.

The first call to make is the one that tells you who the platform thinks you
are:

```
curl -H "X-API-Key: $MCP_API_KEY" https://$HOST/api/v1/portal/me
```

which answers:

```json
{
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "analyst@example.com",
  "roles": ["analyst", "data_engineer"],
  "persona": "analyst",
  "is_admin": false,
  "tools": ["trino_query", "datahub_search"]
}
```

`persona` is the one field to read first. A persona decides which tools and
which connections this credential reaches, so a 403 further down this reference
is usually answered here rather than at the route that returned it.

# Authentication

Every route accepts either credential, in either of two headers. A request with
neither is refused with 401.

## API key

```
X-API-Key: <key>
```

An administrator issues one in the portal under Admin > Keys
(`/portal/admin/keys`), choosing the roles it carries. The key value is
returned once, at creation, and never again by any route: the list of keys
shows names and roles only. Keys declared in the deployment's configuration
file are read-only and cannot be revoked through the API.

Hold an API key when the caller is a script, a scheduled job, or an
integration with no person behind it.

## OIDC bearer token

```
Authorization: Bearer <jwt>
```

A JWT, validated against the deployment's identity provider. Either an access
token from the OIDC provider the portal signs in against, or one the
platform's own OAuth 2.1 authorization server issued to an MCP client.

Hold a bearer token when the call acts as a person, so that the roles, the
persona and the audit trail are that person's.

# Calling a connection through the gateway

An upstream API registered as a connection is reachable over REST without an
MCP client. This is what NiFi, Airflow, a cron job or curl come to this
reference for.

```
POST /api/v1/gateway/{connection}/invoke
```

`{connection}` is the connection's name, exactly as `GET /api/v1/apis` lists
it. That route answers with the connections this credential reaches, and
`/apis/{connection}/operations` with what each one offers, so a caller finds
its own targets without holding an administrator's credential. The body says
what to call on that upstream:

```json
{
  "method": "GET",
  "path": "/customers",
  "query_params": {"limit": 50}
}
```

The gateway holds the upstream's credentials, so the request above carries only
the platform's own. The reply is an envelope: the upstream's status code is
`status` inside the body, not on the HTTP status line. See the Gateway section
for what each platform-level status means and for `paginate`, which makes one
call walk every page.

`/invoke-raw` is the same call with the upstream's body streamed back
unbuffered, for a large or binary response. That route does put the upstream's
status on the HTTP status line, because the response is committed before the
status is known.

To browse the same catalog and copy a ready-made call, open `/portal/apis` in
the portal.

# Conventions

## Paging and filtering

A list route pages one of two ways, and each operation's parameters say which.
Most take `limit` and `offset`. The admin list routes take `per_page` and a
1-based `page` instead; there, a page below 1 or a value that is not a number
reads as the first page. Routes that filter by time take RFC3339 timestamps,
and a timestamp that cannot be parsed widens the query rather than failing it.

## Errors

Most routes report an error as an RFC 9457 problem document, sent as
`application/problem+json`:

```json
{
  "type": "about:blank",
  "title": "Not Found",
  "status": 404,
  "detail": "resource not found"
}
```

`type` is `about:blank` unless the platform has a name for the problem, in
which case it is a `urn:mcp-data-platform:problem:` URN that a client can
compare against. `detail` is prose for a person.

The gateway data plane and the managed-resource routes answer with a single
`error` string instead, because that is the shape their MCP counterparts
already return and a client of both should not have to read two:

```json
{"error": "connection \"acme-billing\" is not registered"}
```

Each operation's responses name the body it sends, so the one to expect is
always on the page.

## Whose status code it is

On every route except the gateway's, the HTTP status is the platform's own
answer.

On `POST /gateway/{connection}/invoke` it is still the platform's own answer,
and the upstream's is `status` inside the body. A 404 from the upstream arrives
as HTTP 200 with `"status": 404`. That split is deliberate: it lets a client
route on "the gateway is broken" (502, 504) separately from "the upstream is
unhappy".

## Which replica answered

Every response carries `X-Platform-Instance`, naming the process that served
it. A deployment behind a load balancer runs more than one, and that header is
what makes a reply attributable to one of them.

# The MCP surface is elsewhere

This document describes the REST API only. The Model Context Protocol surface
that AI clients connect to, and the tools it registers, are not in here: MCP is
served over streamable HTTP at the deployment's root, with the legacy SSE
transport at `/sse`. A client that speaks MCP discovers the tools by connecting
and calling `tools/list`, not by reading this reference.
