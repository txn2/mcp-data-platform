// The platform's own OpenAPI document, as the mock server answers for it.
//
// A slice of the document a running platform serves at
// /api/v1/admin/docs/doc.json (internal/apidocs/swagger.json): its header, its
// introduction and security-scheme descriptions, its tag groups, the two
// gateway routes a non-MCP caller invokes a connection through, four more
// routes, and every definition those reference. Copied from the real document
// rather than invented, so the API Reference page is exercised on the shapes it
// is really given -- Swagger 2.0, which ReDoc converts as it loads, $refs that
// have to resolve, and the Gateway tag the operation browser's pointer
// deep-links to.
//
// Which routes are here is decided by the contrast sweep in
// e2e/interactive/api-reference.spec.ts, which can only check what the fixture
// renders. Between them these six routes cover every construct the served
// document contains that ReDoc paints in a color of its own: the five HTTP
// verbs it uses (get, post, put, delete, patch), a 2xx, a 302, a 4xx and a 5xx,
// a 204 with no schema, an enum, allOf, additionalProperties, a security
// requirement, and an info.description carrying headings and fenced code. A
// construct the served document gains and this one lacks is a construct nothing
// is looking at, so adding it here is part of adding it there.
//
// It is a fixture and does not follow the served document. What a deployment
// reads is the document its own binary serves.
export const mockPlatformSpec = {
  swagger: "2.0",
  info: {
    description:
      'The platform\'s REST surface: the admin API, the portal API, and the gateway\nroutes a non-MCP client calls an upstream connection through.\n\nEvery route below authenticates. Almost every one answers JSON; the handful\nthat return a file, an image, a page or YAML say so in their `produces`.\n\n# Getting started\n\nThe base URL is the origin you fetched this document from, followed by\n`/api/v1`. The `host` at the top of this reference is that origin: the\ndocument is rewritten as it is served, so the URLs here are the ones this\ndeployment answers on.\n\nThe first call to make is the one that tells you who the platform thinks you\nare:\n\n```\ncurl -H "X-API-Key: $MCP_API_KEY" https://$HOST/api/v1/portal/me\n```\n\nwhich answers:\n\n```json\n{\n  "user_id": "550e8400-e29b-41d4-a716-446655440000",\n  "email": "analyst@example.com",\n  "roles": ["analyst", "data_engineer"],\n  "persona": "analyst",\n  "is_admin": false,\n  "tools": ["trino_query", "datahub_search"]\n}\n```\n\n`persona` is the one field to read first. A persona decides which tools and\nwhich connections this credential reaches, so a 403 further down this reference\nis usually answered here rather than at the route that returned it.\n\n# Authentication\n\nEvery route accepts either credential, in either of two headers. A request with\nneither is refused with 401.\n\n## API key\n\n```\nX-API-Key: <key>\n```\n\nAn administrator issues one in the portal under Admin > Keys\n(`/portal/admin/keys`), choosing the roles it carries. The key value is\nreturned once, at creation, and never again by any route: the list of keys\nshows names and roles only. Keys declared in the deployment\'s configuration\nfile are read-only and cannot be revoked through the API.\n\nHold an API key when the caller is a script, a scheduled job, or an\nintegration with no person behind it.\n\n## OIDC bearer token\n\n```\nAuthorization: Bearer <jwt>\n```\n\nA JWT, validated against the deployment\'s identity provider. Either an access\ntoken from the OIDC provider the portal signs in against, or one the\nplatform\'s own OAuth 2.1 authorization server issued to an MCP client.\n\nHold a bearer token when the call acts as a person, so that the roles, the\npersona and the audit trail are that person\'s.\n\n# Calling a connection through the gateway\n\nAn upstream API registered as a connection is reachable over REST without an\nMCP client. This is what NiFi, Airflow, a cron job or curl come to this\nreference for.\n\n```\nPOST /api/v1/gateway/{connection}/invoke\n```\n\n`{connection}` is the connection\'s name, exactly as `GET /api/v1/apis` lists\nit. That route answers with the connections this credential reaches, and\n`/apis/{connection}/operations` with what each one offers, so a caller finds\nits own targets without holding an administrator\'s credential. The body says\nwhat to call on that upstream:\n\n```json\n{\n  "method": "GET",\n  "path": "/customers",\n  "query_params": {"limit": 50}\n}\n```\n\nThe gateway holds the upstream\'s credentials, so the request above carries only\nthe platform\'s own. The reply is an envelope: the upstream\'s status code is\n`status` inside the body, not on the HTTP status line. See the Gateway section\nfor what each platform-level status means and for `paginate`, which makes one\ncall walk every page.\n\n`/invoke-raw` is the same call with the upstream\'s body streamed back\nunbuffered, for a large or binary response. That route does put the upstream\'s\nstatus on the HTTP status line, because the response is committed before the\nstatus is known.\n\nTo browse the same catalog and copy a ready-made call, open `/portal/apis` in\nthe portal.\n\n# Conventions\n\n## Paging and filtering\n\nA list route pages one of two ways, and each operation\'s parameters say which.\nMost take `limit` and `offset`. The admin list routes take `per_page` and a\n1-based `page` instead; there, a page below 1 or a value that is not a number\nreads as the first page. Routes that filter by time take RFC3339 timestamps,\nand a timestamp that cannot be parsed widens the query rather than failing it.\n\n## Errors\n\nMost routes report an error as an RFC 9457 problem document, sent as\n`application/problem+json`:\n\n```json\n{\n  "type": "about:blank",\n  "title": "Not Found",\n  "status": 404,\n  "detail": "resource not found"\n}\n```\n\n`type` is `about:blank` unless the platform has a name for the problem, in\nwhich case it is a `urn:mcp-data-platform:problem:` URN that a client can\ncompare against. `detail` is prose for a person.\n\nThe gateway data plane and the managed-resource routes answer with a single\n`error` string instead, because that is the shape their MCP counterparts\nalready return and a client of both should not have to read two:\n\n```json\n{"error": "connection \\"acme-billing\\" is not registered"}\n```\n\nEach operation\'s responses name the body it sends, so the one to expect is\nalways on the page.\n\n## Whose status code it is\n\nOn every route except the gateway\'s, the HTTP status is the platform\'s own\nanswer.\n\nOn `POST /gateway/{connection}/invoke` it is still the platform\'s own answer,\nand the upstream\'s is `status` inside the body. A 404 from the upstream arrives\nas HTTP 200 with `"status": 404`. That split is deliberate: it lets a client\nroute on "the gateway is broken" (502, 504) separately from "the upstream is\nunhappy".\n\n## Which replica answered\n\nEvery response carries `X-Platform-Instance`, naming the process that served\nit. A deployment behind a load balancer runs more than one, and that header is\nwhat makes a reply attributable to one of them.\n\n# The MCP surface is elsewhere\n\nThis document describes the REST API only. The Model Context Protocol surface\nthat AI clients connect to, and the tools it registers, are not in here: MCP is\nserved over streamable HTTP at the deployment\'s root, with the legacy SSE\ntransport at `/sse`. A client that speaks MCP discovers the tools by connecting\nand calling `tools/list`, not by reading this reference.',
    title: "MCP Data Platform API",
    contact: {},
    version: "1.0",
  },
  host: "localhost:8080",
  basePath: "/api/v1",
  securityDefinitions: {
    ApiKeyAuth: {
      type: "apiKey",
      name: "X-API-Key",
      in: "header",
      description:
        "A platform API key, sent as `X-API-Key: <key>`.\n\nAn administrator issues one in the portal under Admin > Keys (`/portal/admin/keys`), choosing the roles it carries. The value is returned once, at creation, and by no route afterwards. Keys declared in the deployment's configuration file are read-only.\n\nHold an API key when the caller is a script, a scheduled job, or an integration with no person behind it.",
    },
    BearerAuth: {
      type: "apiKey",
      name: "Authorization",
      in: "header",
      description:
        "A JWT, sent as `Authorization: Bearer <jwt>`.\n\nEither an access token from the OIDC provider this deployment signs in against, or one the platform's own OAuth 2.1 authorization server issued to an MCP client.\n\nHold a bearer token when the call acts as a person, so the roles, the persona and the audit trail are that person's.",
    },
  },
  "x-tagGroups": [
    {
      name: "User API",
      tags: ["Collections", "Gateway", "Resources"],
    },
    {
      name: "Admin API",
      tags: ["Connections", "System"],
    },
  ],
  tags: [
    {
      name: "Collections",
      description:
        "Curated groups of assets organized into ordered sections with markdown descriptions. Collections support sharing via public links and user-level permissions.",
    },
    {
      name: "Connections",
      description:
        "Toolkit connection management for Trino, DataHub, and S3 backends. View file-configured connections, create database-managed instances, and inspect connection details.",
    },
    {
      name: "Gateway",
      description:
        "The API gateway data plane: call a configured upstream connection over REST, enveloped or streamed. This is what a non-MCP client (NiFi, Airflow, curl) uses in place of the api_invoke_endpoint tool.",
    },
    {
      name: "Resources",
      description:
        "Human-uploaded reference materials \u2014 SQL templates, runbooks, checklists, and brand assets. Scoped by visibility (global, persona, user) and accessible to AI agents via the MCP resources protocol.",
    },
    {
      name: "System",
      description:
        "Platform identity, version, runtime feature availability, registered tools, and toolkit connections.",
    },
  ],
  paths: {
    "/gateway/{connection}/invoke": {
      post: {
        security: [
          {
            ApiKeyAuth: [],
          },
          {
            BearerAuth: [],
          },
        ],
        description:
          'Forwards a request to the named upstream connection and returns the enveloped result. This is the REST equivalent of the api_invoke_endpoint MCP tool, for non-MCP clients (NiFi, Airflow, curl).\n\nThe HTTP status of THIS response reports the platform\'s own outcome only. When the platform performed the call, the response is 200 and the upstream\'s own status code is in `status` inside the body \u2014 a 404 from the upstream arrives as HTTP 200 with `"status": 404`. That split lets a client route on "the gateway is broken" (502, 504) separately from "the upstream is unhappy".\n\nA `connection` key in the body is ignored; the connection is taken from the URL. `operation_id`, `path_params`, `spec` and `decode` are MCP-tool parameters and are not bound on this route.\n\nBrowse connections and copy a ready-made call at /portal/apis.',
        consumes: ["application/json"],
        produces: ["application/json"],
        tags: ["Gateway"],
        summary: "Call an upstream connection through the API gateway",
        parameters: [
          {
            type: "string",
            description: "Name of the configured upstream connection",
            name: "connection",
            in: "path",
            required: true,
          },
          {
            description: "Request to forward upstream",
            name: "body",
            in: "body",
            required: true,
            schema: {
              $ref: "#/definitions/gatewayhttp.invokeRequest",
            },
          },
        ],
        responses: {
          "200": {
            description:
              "The platform performed the call; the upstream's status is in the body",
            schema: {
              $ref: "#/definitions/apigateway.InvokeOutput",
            },
          },
          "400": {
            description:
              "Request failed validation, or paginate was set on a raw route",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "401": {
            description: "No credential, or the credential was rejected",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "403": {
            description: "Persona or route policy denied the call",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "404": {
            description: "The named connection is not registered",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "413": {
            description: "Upstream body exceeds the inline size limit",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "415": {
            description: "Upstream body is not inlineable at its media type",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "429": {
            description: "Inline read budget exhausted; Retry-After is set",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "500": {
            description:
              "The platform could not complete its own side of the call",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "502": {
            description: "The gateway could not reach the upstream",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "504": {
            description: "The upstream call exceeded its deadline",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
        },
      },
    },
    "/gateway/{connection}/invoke-raw": {
      post: {
        security: [
          {
            ApiKeyAuth: [],
          },
          {
            BearerAuth: [],
          },
        ],
        description:
          "Same call as /invoke, but the upstream body is streamed straight to the client instead of being buffered into a JSON envelope (issue #535). Use it for large or binary bodies.\n\nUnlike /invoke, this route puts the UPSTREAM's status code on the HTTP status line, because the response is committed the moment the first byte is streamed. The platform-level codes below apply only to a call that fails before any byte is sent.\n\nForwarded upstream headers: Content-Length, Content-Encoding, Content-Range, Cache-Control, ETag, Last-Modified. Content-Type and Content-Disposition are derived by the platform's content contract rather than passed through, and a response with no upstream Cache-Control is served `private`.\n\n`paginate` is refused on this route: a walk merges JSON pages and there is nothing to merge in a byte stream.",
        consumes: ["application/json"],
        produces: ["application/octet-stream"],
        tags: ["Gateway"],
        summary:
          "Call an upstream connection and stream the body back unbuffered",
        parameters: [
          {
            type: "string",
            description: "Name of the configured upstream connection",
            name: "connection",
            in: "path",
            required: true,
          },
          {
            description: "Request to forward upstream",
            name: "body",
            in: "body",
            required: true,
            schema: {
              $ref: "#/definitions/gatewayhttp.invokeRequest",
            },
          },
        ],
        responses: {
          "200": {
            description:
              "The upstream body, streamed; the status line is the upstream's own",
            schema: {
              type: "file",
            },
          },
          "400": {
            description: "Request failed validation, or paginate was set",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "401": {
            description: "No credential, or the credential was rejected",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "403": {
            description: "Persona or route policy denied the call",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "404": {
            description: "The named connection is not registered",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "413": {
            description: "Upstream body exceeds the raw passthrough cap",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "500": {
            description:
              "The platform could not complete its own side of the call",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "502": {
            description: "The gateway could not reach the upstream",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
          "504": {
            description: "The upstream call exceeded its deadline",
            schema: {
              $ref: "#/definitions/gatewayhttp.errorEnvelope",
            },
          },
        },
      },
    },
    "/admin/connections": {
      get: {
        security: [
          {
            ApiKeyAuth: [],
          },
          {
            BearerAuth: [],
          },
        ],
        description: "Returns all toolkit connections with their tools.",
        produces: ["application/json"],
        tags: ["System"],
        summary: "List connections",
        responses: {
          "200": {
            description: "OK",
            schema: {
              $ref: "#/definitions/admin.connectionListResponse",
            },
          },
        },
      },
    },
    "/resources/{id}": {
      patch: {
        security: [
          {
            ApiKeyAuth: [],
          },
          {
            BearerAuth: [],
          },
        ],
        description:
          "Update mutable metadata fields of a managed resource, and/or refile it by naming a target scope, a target folder path, or both.",
        consumes: ["application/json"],
        produces: ["application/json"],
        tags: ["Resources"],
        summary: "Update resource",
        parameters: [
          {
            type: "string",
            description: "Resource ID",
            name: "id",
            in: "path",
            required: true,
          },
          {
            description: "Fields to update",
            name: "body",
            in: "body",
            required: true,
            schema: {
              $ref: "#/definitions/resource.Update",
            },
          },
        ],
        responses: {
          "200": {
            description: "OK",
            schema: {
              $ref: "#/definitions/github_com_txn2_mcp-data-platform_pkg_resource.Resource",
            },
          },
          "400": {
            description: "Bad Request",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "401": {
            description: "Unauthorized",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "403": {
            description: "Forbidden",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "404": {
            description: "Not Found",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "409": {
            description: "Conflict",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "500": {
            description: "Internal Server Error",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
        },
      },
      delete: {
        security: [
          {
            ApiKeyAuth: [],
          },
          {
            BearerAuth: [],
          },
        ],
        description:
          "Delete a managed resource, removing both the S3 blob and database metadata.",
        tags: ["Resources"],
        summary: "Delete resource",
        parameters: [
          {
            type: "string",
            description: "Resource ID",
            name: "id",
            in: "path",
            required: true,
          },
        ],
        responses: {
          "204": {
            description: "No Content",
          },
          "401": {
            description: "Unauthorized",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "403": {
            description: "Forbidden",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "404": {
            description: "Not Found",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
          "500": {
            description: "Internal Server Error",
            schema: {
              $ref: "#/definitions/resource.errorResponse",
            },
          },
        },
      },
    },
    "/portal/collections/{id}/thumbnail": {
      put: {
        security: [
          {
            ApiKeyAuth: [],
          },
          {
            BearerAuth: [],
          },
        ],
        description:
          "Uploads a PNG thumbnail image for the collection. The owner, an admin, or a collection Editor.",
        consumes: ["image/png"],
        produces: ["application/json"],
        tags: ["Collections"],
        summary: "Upload collection thumbnail",
        parameters: [
          {
            type: "string",
            description: "Collection ID",
            name: "id",
            in: "path",
            required: true,
          },
          {
            description: "PNG image data",
            name: "body",
            in: "body",
            required: true,
            schema: {
              type: "array",
              items: {
                type: "integer",
              },
            },
          },
        ],
        responses: {
          "204": {
            description: "No Content",
          },
          "401": {
            description: "Unauthorized",
            schema: {
              $ref: "#/definitions/portal.problemDetail",
            },
          },
          "403": {
            description: "Forbidden",
            schema: {
              $ref: "#/definitions/portal.problemDetail",
            },
          },
          "404": {
            description: "Not Found",
            schema: {
              $ref: "#/definitions/portal.problemDetail",
            },
          },
          "413": {
            description: "Request Entity Too Large",
            schema: {
              $ref: "#/definitions/portal.problemDetail",
            },
          },
          "500": {
            description: "Internal Server Error",
            schema: {
              $ref: "#/definitions/portal.problemDetail",
            },
          },
          "503": {
            description: "Service Unavailable",
            schema: {
              $ref: "#/definitions/portal.problemDetail",
            },
          },
        },
      },
    },
    "/admin/oauth/callback": {
      get: {
        description:
          "Public endpoint hit by the upstream OAuth provider after the operator authenticates. Exchanges the code for tokens and stores them. Renders an HTML page on error so a stranded browser tab still gives a useful message.",
        produces: ["text/html"],
        tags: ["Connections"],
        summary: "OAuth authorization-code callback",
        parameters: [
          {
            type: "string",
            description: "OAuth authorization code",
            name: "code",
            in: "query",
          },
          {
            type: "string",
            description: "PKCE state token from oauth-start",
            name: "state",
            in: "query",
            required: true,
          },
          {
            type: "string",
            description: "OAuth error code from upstream",
            name: "error",
            in: "query",
          },
          {
            type: "string",
            description: "Human-readable error from upstream",
            name: "error_description",
            in: "query",
          },
        ],
        responses: {
          "302": {
            description: "Found",
          },
          "400": {
            description: "Bad Request",
            schema: {
              $ref: "#/definitions/httpjson.ProblemDetail",
            },
          },
        },
      },
    },
  },
  definitions: {
    "admin.connectionInfo": {
      type: "object",
      properties: {
        connection: {
          type: "string",
          example: "acme-warehouse",
        },
        health: {
          $ref: "#/definitions/toolkit.ConnectionHealthWire",
        },
        hidden_tools: {
          type: "array",
          items: {
            type: "string",
          },
        },
        kind: {
          type: "string",
          example: "trino",
        },
        name: {
          type: "string",
          example: "acme-warehouse",
        },
        tools: {
          type: "array",
          items: {
            type: "string",
          },
          example: ["trino_query", "trino_describe_table", "trino_browse"],
        },
      },
    },
    "admin.connectionListResponse": {
      type: "object",
      properties: {
        connections: {
          type: "array",
          items: {
            $ref: "#/definitions/admin.connectionInfo",
          },
        },
        total: {
          type: "integer",
          example: 5,
        },
      },
    },
    "apigateway.InvokeInput": {
      type: "object",
      properties: {
        body: {},
        connection: {
          type: "string",
        },
        decode: {
          type: "string",
        },
        headers: {
          type: "object",
          additionalProperties: {
            type: "string",
          },
        },
        method: {
          type: "string",
        },
        operation_id: {
          type: "string",
        },
        paginate: {
          description:
            "Paginate, when set, makes the call a page walk (issue #1535): the\ngateway follows the response's pagination signal itself and returns\nthe merged array. Absent, the signal is reported and not followed.",
          allOf: [
            {
              $ref: "#/definitions/apigateway.PaginateInput",
            },
          ],
        },
        path: {
          type: "string",
        },
        path_params: {
          type: "object",
          additionalProperties: {
            type: "string",
          },
        },
        query_params: {
          type: "object",
          additionalProperties: {},
        },
        spec: {
          type: "string",
        },
        timeout_seconds: {
          type: "integer",
        },
      },
    },
    "apigateway.InvokeOutput": {
      type: "object",
      properties: {
        body: {},
        body_bytes: {
          description:
            "BodyBytes is the size of the body as read from the upstream,\nbefore decoding. Reported on every response; zero when no body was\nreturned. It is not what the call cost the model's context: that\nis the rendered result, which max_inline_bytes bounds and which a\ncut body may make smaller than this (issue #1606).",
          type: "integer",
        },
        body_truncated: {
          type: "boolean",
        },
        duration_ms: {
          type: "integer",
        },
        error: {
          type: "string",
        },
        export_arguments: {
          description:
            "ExportArguments is set when Body was cut by the inline budget\n(issue #1587): the api_export arguments that stream this same\ncall into a portal asset. The caller adds a name.",
          allOf: [
            {
              $ref: "#/definitions/apigateway.InvokeInput",
            },
          ],
        },
        headers: {
          type: "object",
          additionalProperties: {
            type: "array",
            items: {
              type: "string",
            },
          },
        },
        hint: {
          description:
            'Hint surfaces operator-actionable advice to the model when the\nresponse itself can\'t carry it \u2014 most importantly the "use\napi_export instead" suggestion when the body exceeded\nmax_inline_bytes. Distinct from Error: Hint is informational,\nthe call still succeeded.',
          type: "string",
        },
        items_merged: {
          type: "integer",
        },
        pages_fetched: {
          type: "integer",
        },
        pagination: {
          description:
            'Pagination is populated when the upstream response carries a\nrecognizable cursor (RFC 5988 Link rel="next", @odata.nextLink,\nnext_cursor, etc). The model uses this to decide whether to\nissue a follow-up call. The gateway does NOT auto-follow so\neach loop iteration stays observable in audit + conversation.',
          allOf: [
            {
              $ref: "#/definitions/apigateway.PaginationInfo",
            },
          ],
        },
        resolved_path: {
          description:
            "ResolvedPath is the concrete request path an operation_id call\nresolved to, after the catalog's base-path prefix and any\npath_params substitution. Populated only for operation_id\naddressing: in the method+path form the caller wrote the path\nitself and echoing it back says nothing.\n\nIt is here because the resolved path is the one input to the\nupstream request the caller never sees, so a misconfigured\ncatalog prefix surfaces only as a generic upstream 400 whose\ncause is invisible from the response (issue #1298). api_export\nalready reports it in its result message; this is the same\nsignal for the buffered path.",
          type: "string",
        },
        status: {
          type: "integer",
        },
        stopped_by: {
          type: "string",
        },
      },
    },
    "apigateway.PaginateInput": {
      type: "object",
      properties: {
        cursor_param: {
          description:
            "CursorParam is the query parameter a body cursor (next_cursor,\nnextPageToken, ...) is sent back as. A cursor signal on a page that\nnames no CursorParam and no PageParam fails the walk.",
          type: "string",
        },
        items: {
          description:
            'Items is the key of the array merged across pages ("data", "items",\n"results", "value"), a dotted path to a nested one ("result.items"),\nor "$" when the page body is the array. Required: guessing the key\nis how a merged result silently becomes a list of envelopes.',
          type: "string",
        },
        max_pages: {
          description:
            'MaxPages bounds the walk. 0 means defaultMaxPages; the ceiling is\nmaxMaxPages. Reaching it is reported as stopped_by "max_pages", with\nthe signal for the next page in `pagination`.',
          type: "integer",
        },
        page_param: {
          description:
            "PageParam is the query parameter advanced when a page carries no\nnext signal (?page=N, ?offset=N). Its starting value must be present\nin query_params; the first page is requested exactly as given.",
          type: "string",
        },
        page_step: {
          description:
            "PageStep is what PageParam is advanced by per page. 1 (the default)\nsuits ?page=N; the page size suits ?offset=N.",
          type: "integer",
        },
      },
    },
    "apigateway.PaginationInfo": {
      type: "object",
      properties: {
        has_more: {
          type: "boolean",
        },
        next_cursor: {
          type: "string",
        },
        next_url: {
          type: "string",
        },
        source: {
          type: "string",
        },
      },
    },
    "gatewayhttp.errorEnvelope": {
      type: "object",
      properties: {
        error: {
          type: "string",
        },
      },
    },
    "gatewayhttp.invokeRequest": {
      type: "object",
      properties: {
        body: {},
        headers: {
          type: "object",
          additionalProperties: {
            type: "string",
          },
        },
        method: {
          type: "string",
        },
        paginate: {
          description:
            "Paginate, when set, makes the call a page walk (issue #1535):\nthe gateway follows the response's pagination signal itself and\nreturns the merged array, exactly as it does for an MCP caller.\nA REST caller reaching a paginated upstream would otherwise have\nto reimplement the follow loop that the gateway already owns.",
          allOf: [
            {
              $ref: "#/definitions/apigateway.PaginateInput",
            },
          ],
        },
        path: {
          type: "string",
        },
        query_params: {
          type: "object",
          additionalProperties: {},
        },
        timeout_seconds: {
          type: "integer",
        },
      },
    },
    "github_com_txn2_mcp-data-platform_pkg_resource.Resource": {
      type: "object",
      properties: {
        created_at: {
          type: "string",
        },
        description: {
          type: "string",
          example: "Step-by-step procedures for ETL pipeline operations",
        },
        display_name: {
          type: "string",
          example: "ETL Runbook",
        },
        filename: {
          type: "string",
          example: "etl-runbook.md",
        },
        id: {
          type: "string",
          example: "res_01HK7R9F",
        },
        last_read_at: {
          description:
            "LastReadAt is when the resource's content was last served through any\nsurface, stamped by the read recorder. NULL means never read since the\ndeployment began auditing reads (#1014). It is the durable answer, unlike\nthe audit-derived Usage.LastReadAt, which is bounded by audit retention.",
          type: "string",
        },
        mime_type: {
          type: "string",
          example: "text/markdown",
        },
        path: {
          description:
            "Path is the slash-separated folder path this resource is filed under\ninside its library, and the tail of its URI ahead of the filename. A\none-segment path is what every resource carried before folders (#1529).",
          type: "string",
          example: "runbooks/etl",
        },
        s3_key: {
          type: "string",
          example: "resources/res_01HK7R9F/etl-runbook.md",
        },
        scope: {
          allOf: [
            {
              $ref: "#/definitions/resource.Scope",
            },
          ],
          example: "persona",
        },
        scope_id: {
          description: "persona name or user sub; empty for global",
          type: "string",
          example: "data-engineer",
        },
        size_bytes: {
          type: "integer",
          example: 34000,
        },
        tags: {
          type: "array",
          items: {
            type: "string",
          },
        },
        thumbnail_captured_at: {
          description:
            "ThumbnailCapturedAt and ThumbnailDarkCapturedAt are when each capture was\ntaken. A capture older than the resource's UpdatedAt is behind the file it\ncame from, which is what the pending list is built on; see migration\n000134 for why this is a timestamp rather than a version.",
          type: "string",
        },
        thumbnail_dark_captured_at: {
          type: "string",
        },
        thumbnail_dark_s3_key: {
          type: "string",
        },
        thumbnail_s3_key: {
          description:
            "ThumbnailS3Key and ThumbnailDarkS3Key are the captured PNGs stored beside\nthe resource's own object, empty until one is taken (#1554). The library\nused to draw the original file scaled down instead, which meant a\nnon-image had no tile at all and an image cost its full size to show.",
          type: "string",
        },
        updated_at: {
          type: "string",
        },
        uploader_email: {
          type: "string",
          example: "marcus.johnson@example.com",
        },
        uploader_sub: {
          type: "string",
          example: "550e8400-e29b-41d4-a716-446655440000",
        },
        uri: {
          type: "string",
          example: "mcp://persona/data-engineer/runbooks/etl-runbook.md",
        },
        usage: {
          description:
            "Usage is the audit-derived read activity of this resource. It is not a\nstored column: the detail read fills it from the audit rollup, and it is\nabsent everywhere the rollup was not consulted.",
          allOf: [
            {
              $ref: "#/definitions/resource.Usage",
            },
          ],
        },
      },
    },
    "httpjson.ProblemDetail": {
      type: "object",
      properties: {
        detail: {
          type: "string",
          example: "resource not found",
        },
        status: {
          type: "integer",
          example: 404,
        },
        title: {
          type: "string",
          example: "Not Found",
        },
        type: {
          type: "string",
          example: "about:blank",
        },
      },
    },
    "portal.problemDetail": {
      type: "object",
      properties: {
        detail: {
          type: "string",
        },
        status: {
          type: "integer",
        },
        title: {
          type: "string",
        },
        type: {
          type: "string",
        },
      },
    },
    "resource.Scope": {
      type: "string",
      enum: ["global", "persona", "user"],
      "x-enum-varnames": ["ScopeGlobal", "ScopePersona", "ScopeUser"],
    },
    "resource.Update": {
      type: "object",
      properties: {
        description: {
          type: "string",
        },
        display_name: {
          type: "string",
        },
        path: {
          description:
            "Path refiles the resource in another folder of its library. Like Scope it\nis not metadata: the folder path is half of the resource's URI, so an edit\nto it rewrites the address and records the one it vacated (#1528).",
          type: "string",
        },
        scope: {
          description:
            "Scope names the library to move the resource into. Nil leaves it where it\nis, which is what every request that is not a move sends.",
          allOf: [
            {
              $ref: "#/definitions/resource.Scope",
            },
          ],
        },
        scope_id: {
          description:
            "ScopeID is the persona name or the user's sub or address, and is empty for\nthe global library. It is read only when Scope is set: a scope id on its\nown names no library.",
          type: "string",
        },
        tags: {
          type: "array",
          items: {
            type: "string",
          },
        },
      },
    },
    "resource.Usage": {
      type: "object",
      properties: {
        by_surface_30d: {
          description:
            "BySurface30d breaks the 30-day count down by Surface* value.",
          type: "object",
          additionalProperties: {
            type: "integer",
            format: "int64",
          },
        },
        last_read_at: {
          description:
            "LastReadAt is the most recent audited read within the retention window.\nThe durable answer lives on the resource row (Resource.LastReadAt), which\noutlives retention; this field is what the rollup itself saw.",
          type: "string",
        },
        reads_30d: {
          description:
            "Reads30d and Reads90d count audited reads in the trailing 30 and 90\ndays. Both are bounded by the audit retention window: a deployment\nkeeping 30 days of audit reports the same number twice.",
          type: "integer",
          example: 42,
        },
        reads_90d: {
          type: "integer",
          example: 117,
        },
      },
    },
    "resource.errorResponse": {
      type: "object",
      properties: {
        error: {
          type: "string",
          example: "descriptive error message",
        },
      },
    },
    "toolkit.ConnectionHealthWire": {
      type: "object",
      properties: {
        last_error: {
          type: "string",
        },
        last_success: {
          type: "string",
        },
        reachable: {
          type: "boolean",
        },
      },
    },
  },
};
