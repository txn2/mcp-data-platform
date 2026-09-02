// The wire shapes of the operation browser (#1478).
//
// Two surfaces feed one page. The caller-scoped routes under /api/v1/apis
// answer what a persona reaches; the admin catalog routes answer what has been
// loaded. Both describe an operation with the same types the platform's own
// tools return, so a pane and `api_discover` cannot disagree.

/** One component spec of a catalog, as a browse surface counts it. */
export interface APISpecSummary {
  name: string;
  title?: string;
  description?: string;
  /** Operations in this spec that the reader reaches. */
  operation_count: number;
  /** Prefix every path in this spec already carries. */
  base_path?: string;
}

/** One api-kind connection the caller reaches. */
export interface APIConnection {
  name: string;
  description?: string;
  /** Upstream root every operation path is joined onto. */
  base_url?: string;
  /** "none", "bearer", "api_key", "basic", or an oauth2 mode. Never a credential. */
  auth_mode?: string;
  catalog_id?: string;
  /** Operations the caller reaches, not the catalog's total. */
  operation_count: number;
  specs: APISpecSummary[];
}

export interface APIConnectionList {
  connections: APIConnection[];
}

/** One row in an operation index. */
export interface APIOperationSummary {
  operation_id: string;
  method: string;
  path: string;
  summary?: string;
  tags?: string[];
  spec?: string;
}

export interface APIOperationList {
  connection: APIConnection;
  operations: APIOperationSummary[];
}

/** The operations of one stored catalog spec, with no connection in scope. */
export interface APISpecOperationList {
  operations: APIOperationSummary[];
  base_path?: string;
}

/** One parameter of an operation. */
export interface APIParameterDetail {
  name: string;
  /** "path", "query", "header", or "cookie". */
  in: string;
  required?: boolean;
  description?: string;
  schema?: unknown;
}

export interface APIRequestBodyDetail {
  required?: boolean;
  description?: string;
  content_types?: string[];
  schema?: unknown;
  examples?: Record<string, unknown>;
}

export interface APIHeaderDetail {
  description?: string;
  required?: boolean;
  schema?: unknown;
}

export interface APIResponseDetail {
  status: string;
  description?: string;
  content_types?: string[];
  headers?: Record<string, APIHeaderDetail>;
  schema?: unknown;
  examples?: Record<string, unknown>;
}

/**
 * A request promoted from a call that actually worked against a connection.
 * Mirrors catalog.Example (pkg/toolkits/apigateway/catalog/examples.go).
 */
export interface APISavedExample {
  id?: string;
  connection?: string;
  operation_id?: string;
  method?: string;
  path?: string;
  /** What the example is called: the purpose stated for the call it came from. */
  name: string;
  description?: string;
  call_record_id?: string;
  created_by?: string;
}

/**
 * One operation in full. This is the operation `api_discover` returns at its operation level:
 * the pane renders the resolution the tool returns rather than a second parse
 * of the same document.
 */
export interface APIOperationDetail {
  spec?: string;
  operation_id: string;
  method: string;
  path: string;
  summary?: string;
  description?: string;
  parameters?: APIParameterDetail[];
  request_body?: APIRequestBodyDetail;
  responses?: APIResponseDetail[];
  examples?: Record<string, unknown>;
  saved_examples?: APISavedExample[];
  /** Set when the resolved schemas were truncated to fit a response cap. */
  note?: string;
}
