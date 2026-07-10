import type {
  APICatalogSummary,
  APICatalogSpec,
  APICatalogEmbeddingHealth,
  APICatalogEmbeddingSpecStatus,
  EmbeddingProviderStatus,
} from "@/api/admin/hooks/catalogs";

// ---------------------------------------------------------------------------
// API Gateway Catalogs — rich demo fixtures
//
// Three real-world vendor API catalogs the ACME data platform connects to via
// the apigateway toolkit. Each catalog bundles one or more OpenAPI 3.0 specs;
// operations are embedded for semantic tool discovery. The GitHub catalog keeps
// one spec in the "running" embedding state so screenshots exercise the
// in-flight progress badge.
// ---------------------------------------------------------------------------

const CREATED_BY = "data-platform@acme.example.com";

// Build a compact but valid OpenAPI 3.0 document as a JSON string so the spec
// viewer renders real paths/summaries. The declared operation_count on each
// spec reflects the full upstream spec; the inline content is a representative
// slice (4-8 operations) to keep the fixture readable.
function openapi(doc: {
  title: string;
  version: string;
  description: string;
  serverUrl: string;
  paths: Record<string, Record<string, { summary: string; operationId: string }>>;
}): string {
  return JSON.stringify(
    {
      openapi: "3.0.3",
      info: {
        title: doc.title,
        version: doc.version,
        description: doc.description,
      },
      servers: [{ url: doc.serverUrl }],
      paths: Object.fromEntries(
        Object.entries(doc.paths).map(([path, methods]) => [
          path,
          Object.fromEntries(
            Object.entries(methods).map(([method, op]) => [
              method,
              {
                summary: op.summary,
                operationId: op.operationId,
                responses: { "200": { description: "OK" } },
              },
            ]),
          ),
        ]),
      ),
    },
    null,
    2,
  );
}

// ---------------------------------------------------------------------------
// Catalog summaries — GET /api-catalogs and GET /api-catalogs/:id
// ---------------------------------------------------------------------------

export const mockCatalogs: APICatalogSummary[] = [
  {
    id: "salesforce-rest-2025-01",
    name: "salesforce-rest",
    version: "59.0",
    display_name: "Salesforce REST API",
    description:
      "Salesforce Platform REST API — CRUD over SObjects plus SOQL/SOSL query endpoints. Powers the ACME revenue-operations connections.",
    created_by: CREATED_BY,
    created_at: "2025-01-06T09:14:00Z",
    updated_at: "2025-01-22T16:41:00Z",
    spec_count: 2,
    ref_count: 2,
  },
  {
    id: "stripe-api-2025-01",
    name: "stripe-api",
    version: "2024-11-20",
    display_name: "Stripe API",
    description:
      "Stripe payments and billing API — charges, customers, invoices, subscriptions, and usage records. Backs the ACME finance connection.",
    created_by: CREATED_BY,
    created_at: "2025-01-09T11:30:00Z",
    updated_at: "2025-01-20T13:07:00Z",
    spec_count: 2,
    ref_count: 1,
  },
  {
    id: "github-rest-2025-01",
    name: "github-rest",
    version: "2022-11-28",
    display_name: "GitHub REST API",
    description:
      "GitHub REST API — repositories, commits, pull requests, and issues. Used by the ACME engineering-analytics connection.",
    created_by: CREATED_BY,
    created_at: "2025-01-15T15:52:00Z",
    updated_at: "2025-01-23T10:18:00Z",
    spec_count: 1,
    ref_count: 1,
  },
];

// ---------------------------------------------------------------------------
// Per-spec content — GET /api-catalogs/:id/specs/:specName
// Keyed catalogId -> specName -> APICatalogSpec.
// ---------------------------------------------------------------------------

export const mockCatalogSpecs: Record<string, Record<string, APICatalogSpec>> = {
  "salesforce-rest-2025-01": {
    sobjects: {
      spec_name: "sobjects",
      source_kind: "url",
      source_url:
        "https://developer.salesforce.com/openapi/v59.0/sobjects.json",
      etag: "\"sf-sobjects-9f3ac21\"",
      base_path: "/services/data/v59.0",
      title: "Salesforce SObjects",
      description:
        "Record-level CRUD across standard and custom SObjects (Account, Contact, Opportunity, Lead, and custom __c objects).",
      content: openapi({
        title: "Salesforce SObjects",
        version: "v59.0",
        description: "Record-level CRUD across standard and custom SObjects.",
        serverUrl: "https://acme.my.salesforce.com/services/data/v59.0",
        paths: {
          "/sobjects": {
            get: {
              summary: "List available SObjects",
              operationId: "listSObjects",
            },
          },
          "/sobjects/{sobject}/describe": {
            get: {
              summary: "Describe an SObject's fields and metadata",
              operationId: "describeSObject",
            },
          },
          "/sobjects/{sobject}": {
            post: {
              summary: "Create a record",
              operationId: "createRecord",
            },
          },
          "/sobjects/{sobject}/{id}": {
            get: { summary: "Retrieve a record by ID", operationId: "getRecord" },
            patch: {
              summary: "Update a record by ID",
              operationId: "updateRecord",
            },
            delete: {
              summary: "Delete a record by ID",
              operationId: "deleteRecord",
            },
          },
        },
      }),
      last_fetched_at: "2025-01-22T16:41:00Z",
      created_at: "2025-01-06T09:14:00Z",
      updated_at: "2025-01-22T16:41:00Z",
      operation_count: 78,
      embedding_count: 78,
      embedding_status: "succeeded",
      embedding_attempts: 1,
    },
    query: {
      spec_name: "query",
      source_kind: "url",
      source_url: "https://developer.salesforce.com/openapi/v59.0/query.json",
      etag: "\"sf-query-4b71de0\"",
      base_path: "/services/data/v59.0",
      title: "Salesforce Query",
      description:
        "SOQL and SOSL query execution, including cursor-based pagination for large result sets.",
      content: openapi({
        title: "Salesforce Query",
        version: "v59.0",
        description: "SOQL and SOSL query execution.",
        serverUrl: "https://acme.my.salesforce.com/services/data/v59.0",
        paths: {
          "/query": {
            get: { summary: "Execute a SOQL query", operationId: "query" },
          },
          "/queryAll": {
            get: {
              summary: "Execute a SOQL query including deleted records",
              operationId: "queryAll",
            },
          },
          "/query/{queryLocator}": {
            get: {
              summary: "Retrieve the next batch of query results",
              operationId: "queryMore",
            },
          },
          "/search": {
            get: { summary: "Execute a SOSL search", operationId: "search" },
          },
        },
      }),
      last_fetched_at: "2025-01-22T16:41:00Z",
      created_at: "2025-01-06T09:14:00Z",
      updated_at: "2025-01-18T08:55:00Z",
      operation_count: 42,
      embedding_count: 42,
      embedding_status: "succeeded",
      embedding_attempts: 1,
    },
  },

  "stripe-api-2025-01": {
    core: {
      spec_name: "core",
      source_kind: "url",
      source_url: "https://raw.githubusercontent.com/stripe/openapi/master/openapi/spec3.json",
      etag: "\"stripe-core-77c0aa5\"",
      base_path: "/v1",
      title: "Stripe Core",
      description:
        "Core payments primitives — customers, charges, payment intents, and refunds.",
      content: openapi({
        title: "Stripe Core",
        version: "2024-11-20",
        description: "Core payments primitives.",
        serverUrl: "https://api.stripe.com/v1",
        paths: {
          "/customers": {
            get: { summary: "List customers", operationId: "listCustomers" },
            post: { summary: "Create a customer", operationId: "createCustomer" },
          },
          "/customers/{customer}": {
            get: { summary: "Retrieve a customer", operationId: "getCustomer" },
          },
          "/charges": {
            get: { summary: "List charges", operationId: "listCharges" },
            post: { summary: "Create a charge", operationId: "createCharge" },
          },
          "/payment_intents": {
            post: {
              summary: "Create a payment intent",
              operationId: "createPaymentIntent",
            },
          },
        },
      }),
      last_fetched_at: "2025-01-20T13:07:00Z",
      created_at: "2025-01-09T11:30:00Z",
      updated_at: "2025-01-20T13:07:00Z",
      operation_count: 54,
      embedding_count: 54,
      embedding_status: "succeeded",
      embedding_attempts: 1,
    },
    billing: {
      spec_name: "billing",
      source_kind: "upload",
      etag: "\"stripe-billing-1d92fb3\"",
      base_path: "/v1",
      title: "Stripe Billing",
      description:
        "Subscription billing — invoices, subscriptions, prices, and metered usage records.",
      content: openapi({
        title: "Stripe Billing",
        version: "2024-11-20",
        description: "Subscription billing endpoints.",
        serverUrl: "https://api.stripe.com/v1",
        paths: {
          "/invoices": {
            get: { summary: "List invoices", operationId: "listInvoices" },
            post: { summary: "Create an invoice", operationId: "createInvoice" },
          },
          "/subscriptions": {
            get: {
              summary: "List subscriptions",
              operationId: "listSubscriptions",
            },
            post: {
              summary: "Create a subscription",
              operationId: "createSubscription",
            },
          },
          "/prices": {
            get: { summary: "List prices", operationId: "listPrices" },
          },
          "/billing/meter_events": {
            post: {
              summary: "Record a metered usage event",
              operationId: "createMeterEvent",
            },
          },
        },
      }),
      last_fetched_at: "2025-01-16T10:12:00Z",
      created_at: "2025-01-09T11:30:00Z",
      updated_at: "2025-01-16T10:12:00Z",
      operation_count: 36,
      embedding_count: 36,
      embedding_status: "succeeded",
      embedding_attempts: 1,
    },
  },

  "github-rest-2025-01": {
    repos: {
      spec_name: "repos",
      source_kind: "url",
      source_url:
        "https://raw.githubusercontent.com/github/rest-api-description/main/descriptions/api.github.com/api.github.com.json",
      etag: "\"gh-repos-a01ff28\"",
      base_path: "",
      title: "GitHub Repositories",
      description:
        "Repository, commit, pull-request, and issue endpoints for engineering analytics.",
      content: openapi({
        title: "GitHub Repositories",
        version: "2022-11-28",
        description: "Repository, commit, pull-request, and issue endpoints.",
        serverUrl: "https://api.github.com",
        paths: {
          "/repos/{owner}/{repo}": {
            get: { summary: "Get a repository", operationId: "getRepo" },
          },
          "/repos/{owner}/{repo}/commits": {
            get: { summary: "List commits", operationId: "listCommits" },
          },
          "/repos/{owner}/{repo}/pulls": {
            get: {
              summary: "List pull requests",
              operationId: "listPullRequests",
            },
            post: {
              summary: "Create a pull request",
              operationId: "createPullRequest",
            },
          },
          "/repos/{owner}/{repo}/issues": {
            get: { summary: "List issues", operationId: "listIssues" },
            post: { summary: "Create an issue", operationId: "createIssue" },
          },
        },
      }),
      last_fetched_at: "2025-01-23T10:18:00Z",
      created_at: "2025-01-15T15:52:00Z",
      updated_at: "2025-01-23T10:18:00Z",
      operation_count: 61,
      embedding_count: 44,
      embedding_status: "running",
      embedding_attempts: 1,
    },
  },
};

// ---------------------------------------------------------------------------
// Catalog-level embedding health — GET /api-catalogs/:id/embedding-health
// ---------------------------------------------------------------------------

export const mockCatalogEmbeddingHealth: Record<string, APICatalogEmbeddingHealth> = {
  "salesforce-rest-2025-01": {
    catalog_id: "salesforce-rest-2025-01",
    specs_total: 2,
    specs_indexed: 2,
    specs_pending: 0,
    specs_running: 0,
    specs_failed: 0,
  },
  "stripe-api-2025-01": {
    catalog_id: "stripe-api-2025-01",
    specs_total: 2,
    specs_indexed: 2,
    specs_pending: 0,
    specs_running: 0,
    specs_failed: 0,
  },
  "github-rest-2025-01": {
    catalog_id: "github-rest-2025-01",
    specs_total: 1,
    specs_indexed: 0,
    specs_pending: 0,
    specs_running: 1,
    specs_failed: 0,
  },
};

// ---------------------------------------------------------------------------
// Per-spec embedding status — GET /api-catalogs/:id/embedding-status
// ---------------------------------------------------------------------------

export const mockCatalogEmbeddingStatuses: Record<
  string,
  APICatalogEmbeddingSpecStatus[]
> = {
  "salesforce-rest-2025-01": [
    {
      spec_name: "sobjects",
      operation_count: 78,
      embedding_count: 78,
      job_status: "succeeded",
      job_attempts: 1,
      job_updated_at: "2025-01-22T16:42:10Z",
    },
    {
      spec_name: "query",
      operation_count: 42,
      embedding_count: 42,
      job_status: "succeeded",
      job_attempts: 1,
      job_updated_at: "2025-01-18T08:56:03Z",
    },
  ],
  "stripe-api-2025-01": [
    {
      spec_name: "core",
      operation_count: 54,
      embedding_count: 54,
      job_status: "succeeded",
      job_attempts: 1,
      job_updated_at: "2025-01-20T13:08:22Z",
    },
    {
      spec_name: "billing",
      operation_count: 36,
      embedding_count: 36,
      job_status: "succeeded",
      job_attempts: 1,
      job_updated_at: "2025-01-16T10:13:41Z",
    },
  ],
  "github-rest-2025-01": [
    {
      spec_name: "repos",
      operation_count: 61,
      embedding_count: 44,
      embedded_so_far: 44,
      job_status: "running",
      job_attempts: 1,
      job_updated_at: "2025-01-23T10:19:05Z",
    },
  ],
};

// ---------------------------------------------------------------------------
// Platform-wide embedding provider status — GET /embedding/status
// ---------------------------------------------------------------------------

export const mockEmbeddingProviderStatus: EmbeddingProviderStatus = {
  kind: "openai",
  model: "text-embedding-3-small",
  dimension: 1536,
  status: "ok",
};
