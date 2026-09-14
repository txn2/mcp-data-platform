// The platform's own OpenAPI document, as the mock server answers for it.
//
// A slice of the document a running platform serves at
// /api/v1/admin/docs/doc.json (internal/apidocs/swagger.json): its header and
// security definitions, its two tag groups, the two gateway routes a non-MCP
// caller invokes a connection through, one admin route, and every definition
// those reference. Copied from the real document rather than invented, so the
// API Reference page is exercised on the shapes it is really given -- Swagger
// 2.0, which ReDoc converts as it loads, $refs that have to resolve, and the
// Gateway tag the operation browser's pointer deep-links to.
//
// It is a fixture and does not follow the served document. What a deployment
// reads is the document its own binary serves.
export const mockPlatformSpec = {
  "swagger": "2.0",
  "info": {
    "description": "REST API for the MCP Data Platform. Covers admin endpoints (system, config, personas, auth keys, audit, knowledge, memory, connections), portal endpoints (assets, collections, shares, prompts, activity), and resource management.",
    "title": "MCP Data Platform API",
    "contact": {},
    "version": "1.0"
  },
  "host": "localhost:8080",
  "basePath": "/api/v1",
  "securityDefinitions": {
    "ApiKeyAuth": {
      "type": "apiKey",
      "name": "X-API-Key",
      "in": "header"
    },
    "BearerAuth": {
      "type": "apiKey",
      "name": "Authorization",
      "in": "header"
    }
  },
  "x-tagGroups": [
    {
      "name": "User API",
      "tags": [
        "Gateway"
      ]
    },
    {
      "name": "Admin API",
      "tags": [
        "System"
      ]
    }
  ],
  "tags": [
    {
      "name": "System",
      "description": "Platform identity, version, runtime feature availability, registered tools, and toolkit connections."
    },
    {
      "name": "Gateway",
      "description": "The API gateway data plane: call a configured upstream connection over REST, enveloped or streamed. This is what a non-MCP client (NiFi, Airflow, curl) uses in place of the api_invoke_endpoint tool."
    }
  ],
  "paths": {
    "/gateway/{connection}/invoke": {
      "post": {
        "security": [
          {
            "ApiKeyAuth": []
          },
          {
            "BearerAuth": []
          }
        ],
        "description": "Forwards a request to the named upstream connection and returns the enveloped result. This is the REST equivalent of the api_invoke_endpoint MCP tool, for non-MCP clients (NiFi, Airflow, curl).\n\nThe HTTP status of THIS response reports the platform's own outcome only. When the platform performed the call, the response is 200 and the upstream's own status code is in `status` inside the body \u2014 a 404 from the upstream arrives as HTTP 200 with `\"status\": 404`. That split lets a client route on \"the gateway is broken\" (502, 504) separately from \"the upstream is unhappy\".\n\nA `connection` key in the body is ignored; the connection is taken from the URL. `operation_id`, `path_params`, `spec` and `decode` are MCP-tool parameters and are not bound on this route.\n\nBrowse connections and copy a ready-made call at /portal/apis.",
        "consumes": [
          "application/json"
        ],
        "produces": [
          "application/json"
        ],
        "tags": [
          "Gateway"
        ],
        "summary": "Call an upstream connection through the API gateway",
        "parameters": [
          {
            "type": "string",
            "description": "Name of the configured upstream connection",
            "name": "connection",
            "in": "path",
            "required": true
          },
          {
            "description": "Request to forward upstream",
            "name": "body",
            "in": "body",
            "required": true,
            "schema": {
              "$ref": "#/definitions/gatewayhttp.invokeRequest"
            }
          }
        ],
        "responses": {
          "200": {
            "description": "The platform performed the call; the upstream's status is in the body",
            "schema": {
              "$ref": "#/definitions/apigateway.InvokeOutput"
            }
          },
          "400": {
            "description": "Request failed validation, or paginate was set on a raw route",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "401": {
            "description": "No credential, or the credential was rejected",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "403": {
            "description": "Persona or route policy denied the call",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "404": {
            "description": "The named connection is not registered",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "413": {
            "description": "Upstream body exceeds the inline size limit",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "415": {
            "description": "Upstream body is not inlineable at its media type",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "429": {
            "description": "Inline read budget exhausted; Retry-After is set",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "500": {
            "description": "The platform could not complete its own side of the call",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "502": {
            "description": "The gateway could not reach the upstream",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "504": {
            "description": "The upstream call exceeded its deadline",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          }
        }
      }
    },
    "/gateway/{connection}/invoke-raw": {
      "post": {
        "security": [
          {
            "ApiKeyAuth": []
          },
          {
            "BearerAuth": []
          }
        ],
        "description": "Same call as /invoke, but the upstream body is streamed straight to the client instead of being buffered into a JSON envelope (issue #535). Use it for large or binary bodies.\n\nUnlike /invoke, this route puts the UPSTREAM's status code on the HTTP status line, because the response is committed the moment the first byte is streamed. The platform-level codes below apply only to a call that fails before any byte is sent.\n\nForwarded upstream headers: Content-Length, Content-Encoding, Content-Range, Cache-Control, ETag, Last-Modified. Content-Type and Content-Disposition are derived by the platform's content contract rather than passed through, and a response with no upstream Cache-Control is served `private`.\n\n`paginate` is refused on this route: a walk merges JSON pages and there is nothing to merge in a byte stream.",
        "consumes": [
          "application/json"
        ],
        "produces": [
          "application/octet-stream"
        ],
        "tags": [
          "Gateway"
        ],
        "summary": "Call an upstream connection and stream the body back unbuffered",
        "parameters": [
          {
            "type": "string",
            "description": "Name of the configured upstream connection",
            "name": "connection",
            "in": "path",
            "required": true
          },
          {
            "description": "Request to forward upstream",
            "name": "body",
            "in": "body",
            "required": true,
            "schema": {
              "$ref": "#/definitions/gatewayhttp.invokeRequest"
            }
          }
        ],
        "responses": {
          "200": {
            "description": "The upstream body, streamed; the status line is the upstream's own",
            "schema": {
              "type": "file"
            }
          },
          "400": {
            "description": "Request failed validation, or paginate was set",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "401": {
            "description": "No credential, or the credential was rejected",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "403": {
            "description": "Persona or route policy denied the call",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "404": {
            "description": "The named connection is not registered",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "413": {
            "description": "Upstream body exceeds the raw passthrough cap",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "500": {
            "description": "The platform could not complete its own side of the call",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "502": {
            "description": "The gateway could not reach the upstream",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          },
          "504": {
            "description": "The upstream call exceeded its deadline",
            "schema": {
              "$ref": "#/definitions/gatewayhttp.errorEnvelope"
            }
          }
        }
      }
    },
    "/admin/connections": {
      "get": {
        "security": [
          {
            "ApiKeyAuth": []
          },
          {
            "BearerAuth": []
          }
        ],
        "description": "Returns all toolkit connections with their tools.",
        "produces": [
          "application/json"
        ],
        "tags": [
          "System"
        ],
        "summary": "List connections",
        "responses": {
          "200": {
            "description": "OK",
            "schema": {
              "$ref": "#/definitions/admin.connectionListResponse"
            }
          }
        }
      }
    }
  },
  "definitions": {
    "admin.connectionInfo": {
      "type": "object",
      "properties": {
        "connection": {
          "type": "string",
          "example": "acme-warehouse"
        },
        "health": {
          "$ref": "#/definitions/toolkit.ConnectionHealthWire"
        },
        "hidden_tools": {
          "type": "array",
          "items": {
            "type": "string"
          }
        },
        "kind": {
          "type": "string",
          "example": "trino"
        },
        "name": {
          "type": "string",
          "example": "acme-warehouse"
        },
        "tools": {
          "type": "array",
          "items": {
            "type": "string"
          },
          "example": [
            "trino_query",
            "trino_describe_table",
            "trino_browse"
          ]
        }
      }
    },
    "admin.connectionListResponse": {
      "type": "object",
      "properties": {
        "connections": {
          "type": "array",
          "items": {
            "$ref": "#/definitions/admin.connectionInfo"
          }
        },
        "total": {
          "type": "integer",
          "example": 5
        }
      }
    },
    "apigateway.InvokeInput": {
      "type": "object",
      "properties": {
        "body": {},
        "connection": {
          "type": "string"
        },
        "decode": {
          "type": "string"
        },
        "headers": {
          "type": "object",
          "additionalProperties": {
            "type": "string"
          }
        },
        "method": {
          "type": "string"
        },
        "operation_id": {
          "type": "string"
        },
        "paginate": {
          "description": "Paginate, when set, makes the call a page walk (issue #1535): the\ngateway follows the response's pagination signal itself and returns\nthe merged array. Absent, the signal is reported and not followed.",
          "allOf": [
            {
              "$ref": "#/definitions/apigateway.PaginateInput"
            }
          ]
        },
        "path": {
          "type": "string"
        },
        "path_params": {
          "type": "object",
          "additionalProperties": {
            "type": "string"
          }
        },
        "query_params": {
          "type": "object",
          "additionalProperties": {}
        },
        "spec": {
          "type": "string"
        },
        "timeout_seconds": {
          "type": "integer"
        }
      }
    },
    "apigateway.InvokeOutput": {
      "type": "object",
      "properties": {
        "body": {},
        "body_bytes": {
          "description": "BodyBytes is the size of the body as read from the upstream,\nbefore decoding. Reported on every response; zero when no body was\nreturned. It is not what the call cost the model's context: that\nis the rendered result, which max_inline_bytes bounds and which a\ncut body may make smaller than this (issue #1606).",
          "type": "integer"
        },
        "body_truncated": {
          "type": "boolean"
        },
        "duration_ms": {
          "type": "integer"
        },
        "error": {
          "type": "string"
        },
        "export_arguments": {
          "description": "ExportArguments is set when Body was cut by the inline budget\n(issue #1587): the api_export arguments that stream this same\ncall into a portal asset. The caller adds a name.",
          "allOf": [
            {
              "$ref": "#/definitions/apigateway.InvokeInput"
            }
          ]
        },
        "headers": {
          "type": "object",
          "additionalProperties": {
            "type": "array",
            "items": {
              "type": "string"
            }
          }
        },
        "hint": {
          "description": "Hint surfaces operator-actionable advice to the model when the\nresponse itself can't carry it \u2014 most importantly the \"use\napi_export instead\" suggestion when the body exceeded\nmax_inline_bytes. Distinct from Error: Hint is informational,\nthe call still succeeded.",
          "type": "string"
        },
        "items_merged": {
          "type": "integer"
        },
        "pages_fetched": {
          "type": "integer"
        },
        "pagination": {
          "description": "Pagination is populated when the upstream response carries a\nrecognizable cursor (RFC 5988 Link rel=\"next\", @odata.nextLink,\nnext_cursor, etc). The model uses this to decide whether to\nissue a follow-up call. The gateway does NOT auto-follow so\neach loop iteration stays observable in audit + conversation.",
          "allOf": [
            {
              "$ref": "#/definitions/apigateway.PaginationInfo"
            }
          ]
        },
        "resolved_path": {
          "description": "ResolvedPath is the concrete request path an operation_id call\nresolved to, after the catalog's base-path prefix and any\npath_params substitution. Populated only for operation_id\naddressing: in the method+path form the caller wrote the path\nitself and echoing it back says nothing.\n\nIt is here because the resolved path is the one input to the\nupstream request the caller never sees, so a misconfigured\ncatalog prefix surfaces only as a generic upstream 400 whose\ncause is invisible from the response (issue #1298). api_export\nalready reports it in its result message; this is the same\nsignal for the buffered path.",
          "type": "string"
        },
        "status": {
          "type": "integer"
        },
        "stopped_by": {
          "type": "string"
        }
      }
    },
    "apigateway.PaginateInput": {
      "type": "object",
      "properties": {
        "cursor_param": {
          "description": "CursorParam is the query parameter a body cursor (next_cursor,\nnextPageToken, ...) is sent back as. A cursor signal on a page that\nnames no CursorParam and no PageParam fails the walk.",
          "type": "string"
        },
        "items": {
          "description": "Items is the key of the array merged across pages (\"data\", \"items\",\n\"results\", \"value\"), a dotted path to a nested one (\"result.items\"),\nor \"$\" when the page body is the array. Required: guessing the key\nis how a merged result silently becomes a list of envelopes.",
          "type": "string"
        },
        "max_pages": {
          "description": "MaxPages bounds the walk. 0 means defaultMaxPages; the ceiling is\nmaxMaxPages. Reaching it is reported as stopped_by \"max_pages\", with\nthe signal for the next page in `pagination`.",
          "type": "integer"
        },
        "page_param": {
          "description": "PageParam is the query parameter advanced when a page carries no\nnext signal (?page=N, ?offset=N). Its starting value must be present\nin query_params; the first page is requested exactly as given.",
          "type": "string"
        },
        "page_step": {
          "description": "PageStep is what PageParam is advanced by per page. 1 (the default)\nsuits ?page=N; the page size suits ?offset=N.",
          "type": "integer"
        }
      }
    },
    "apigateway.PaginationInfo": {
      "type": "object",
      "properties": {
        "has_more": {
          "type": "boolean"
        },
        "next_cursor": {
          "type": "string"
        },
        "next_url": {
          "type": "string"
        },
        "source": {
          "type": "string"
        }
      }
    },
    "gatewayhttp.errorEnvelope": {
      "type": "object",
      "properties": {
        "error": {
          "type": "string"
        }
      }
    },
    "gatewayhttp.invokeRequest": {
      "type": "object",
      "properties": {
        "body": {},
        "headers": {
          "type": "object",
          "additionalProperties": {
            "type": "string"
          }
        },
        "method": {
          "type": "string"
        },
        "paginate": {
          "description": "Paginate, when set, makes the call a page walk (issue #1535):\nthe gateway follows the response's pagination signal itself and\nreturns the merged array, exactly as it does for an MCP caller.\nA REST caller reaching a paginated upstream would otherwise have\nto reimplement the follow loop that the gateway already owns.",
          "allOf": [
            {
              "$ref": "#/definitions/apigateway.PaginateInput"
            }
          ]
        },
        "path": {
          "type": "string"
        },
        "query_params": {
          "type": "object",
          "additionalProperties": {}
        },
        "timeout_seconds": {
          "type": "integer"
        }
      }
    },
    "toolkit.ConnectionHealthWire": {
      "type": "object",
      "properties": {
        "last_error": {
          "type": "string"
        },
        "last_success": {
          "type": "string"
        },
        "reachable": {
          "type": "boolean"
        }
      }
    }
  }
};
