import type { GraphQLSchemaInfo } from "@/api/admin/hooks";
import type {
  ConnectionInstance,
  ConnectionsOAuthHealthResponse,
  ConnectionOAuthStatus,
  ConnectionAuthEvent,
  GatewayConnectionStatus,
} from "@/api/admin/types";

// Connection instances for the Admin -> Connections master/detail view.
// Names and kinds intentionally mirror the acme-* connections used across the
// effective-connections list (handlers.ts), system.ts, and enrichment.ts so a
// connection referenced on one page resolves to the same instance on another.
//
// These back the DB-managed read path: GET /connection-instances (list) and
// GET /connection-instances/:kind/:name (detail). The kinds span the four
// toolkit families (trino, s3, datahub) plus the gateway families (mcp, api).
export const mockConnectionInstances: ConnectionInstance[] = [
  {
    kind: "trino",
    name: "acme-warehouse",
    config: {
      host: "trino.internal",
      port: 8443,
      catalog: "warehouse",
      user: "mcp-data-platform",
      tls: true,
      // Exercises the DataHub Integration section of the viewer.
      datahub_source_name: "trino-prod",
      catalog_mapping: {
        warehouse: "prod_warehouse",
        analytics: "prod_analytics",
      },
    },
    description:
      "Production data warehouse with the **retail**, **inventory**, and **analytics** schemas.\n\nBacks the analyst persona's day-to-day querying and is the default `trino_query` target.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-19T16:40:00Z",
  },
  {
    kind: "trino",
    name: "acme-staging",
    config: {
      host: "trino-staging.internal",
      port: 8443,
      catalog: "warehouse",
      user: "mcp-data-platform",
      tls: true,
    },
    description:
      "Staging Trino for testing schema changes and ETL pipelines before they reach `acme-warehouse`.",
    created_by: "sarah.chen@acme.example.com",
    updated_at: "2025-01-14T09:12:00Z",
  },
  {
    kind: "datahub",
    name: "acme-catalog",
    config: {
      server_url: "https://datahub.internal:8080",
      timeout: "30s",
    },
    description:
      "Production metadata catalog: business glossary, ownership, and column-level lineage.\n\nSource of the semantic context cross-enrichment attaches to Trino and gateway responses.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-11T13:05:00Z",
  },
  {
    kind: "datahub",
    name: "acme-catalog-staging",
    config: {
      server_url: "https://datahub-staging.internal:8080",
      timeout: "30s",
    },
    description:
      "Staging metadata catalog for validating ingestion recipes before promotion.",
    created_by: "marcus.johnson@acme.example.com",
    updated_at: "2025-01-08T18:44:00Z",
  },
  {
    kind: "s3",
    name: "acme-data-lake",
    config: {
      bucket: "acme-data-lake-prod",
      region: "us-west-2",
      endpoint: "https://s3.us-west-2.amazonaws.com",
      prefix: "curated/",
    },
    description:
      "Raw data lake: ETL outputs, CDC streams, and ML training data.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-05T11:20:00Z",
  },
  {
    kind: "s3",
    name: "acme-reports",
    config: {
      bucket: "acme-reports-prod",
      region: "us-west-2",
      endpoint: "https://s3.us-west-2.amazonaws.com",
      prefix: "exports/",
    },
    description:
      "Generated reports and exported dashboards for stakeholder distribution.",
    created_by: "sarah.chen@acme.example.com",
    updated_at: "2025-01-17T08:02:00Z",
  },
  {
    kind: "mcp",
    name: "acme-crm-gateway",
    config: {
      endpoint: "https://crm-mcp.internal:9000/mcp",
      auth_mode: "oauth",
      oauth_grant: "authorization_code",
      oauth_authorization_url: "https://auth.acme.example.com/oauth2/authorize",
      oauth_token_url: "https://auth.acme.example.com/oauth2/token",
      oauth_client_id: "acme-crm-gateway",
      oauth_client_secret: "[REDACTED]",
      oauth_scope: "crm.read crm.write",
    },
    description:
      "Gateway-proxied CRM MCP server. Responses are auto-enriched with DataHub context and Trino query availability via the cross-enrichment rules attached to this connection.",
    created_by: "sarah.chen@acme.example.com",
    updated_at: "2025-01-21T15:30:00Z",
  },
  {
    kind: "mcp",
    name: "acme-support-gateway",
    config: {
      endpoint: "https://support-mcp.internal:9100/mcp",
      auth_mode: "oauth",
      oauth_grant: "client_credentials",
      oauth_token_url: "https://auth.acme.example.com/oauth2/token",
      oauth_client_id: "acme-support-gateway",
      oauth_client_secret: "[REDACTED]",
      oauth_scope: "support.read",
    },
    description:
      "Gateway-proxied support-desk MCP server exposing ticket search and SLA lookups.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-20T10:15:00Z",
  },
  {
    // An api-kind OAuth connection in the canonical shape: auth_mode "oauth"
    // with the grant in oauth_grant, which is what migration 000050 produced
    // and what the admin API persists. The editor renders the whole OAuth
    // block for it (#1681).
    kind: "api",
    name: "acme-billing-api",
    config: {
      base_url: "https://billing.internal/api/v1",
      auth_mode: "oauth",
      oauth_grant: "authorization_code",
      oauth_authorization_url: "https://auth.acme.example.com/oauth2/authorize",
      oauth_token_url: "https://auth.acme.example.com/oauth2/token",
      oauth_client_id: "acme-billing-api",
      oauth_client_secret: "[REDACTED]",
      oauth_scope: "billing.read",
      oauth_endpoint_auth_style: "params",
    },
    description:
      "HTTP API gateway over the internal billing service. Exposes invoice and subscription lookups as a single `api_invoke` tool with discovery.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-18T14:48:00Z",
  },
  {
    // A connection carrying both OAuth vocabularies, as a deployment
    // configured before they unified still can: the canonical keys are what it
    // authenticates with and the oauth2_* ones are inert. The viewer strikes
    // the inert values through and the OAuth status card says so (#1682).
    kind: "api",
    name: "acme-analytics-api",
    config: {
      base_url: "https://analytics.internal/api/v1",
      auth_mode: "oauth",
      oauth_grant: "authorization_code",
      oauth_authorization_url: "https://auth.acme.example.com/oauth2/authorize",
      oauth_token_url: "https://auth.acme.example.com/oauth2/token",
      oauth_client_id: "PENDING-REPLACE-ME",
      oauth_client_secret: "[REDACTED]",
      oauth_scope: "analytics.readonly",
      oauth2_client_id: "acme-analytics-api",
      oauth2_client_secret: "[REDACTED]",
      oauth2_scopes: ["analytics.readonly"],
    },
    description:
      "Analytics reporting API. Carries both OAuth config vocabularies: the canonical keys are live and the legacy ones are ignored.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-22T11:10:00Z",
  },
  {
    // An upstream that issues an identifier and a signing key and expects the
    // client to mint its own short-lived assertion (#1648). The secret comes
    // back redacted, as it does from the real admin API.
    kind: "api",
    name: "acme-erp-api",
    config: {
      base_url: "https://erp.internal/api1/service",
      auth_mode: "signed_jwt",
      jwt_algorithm: "HS256",
      jwt_client_secret: "[REDACTED]",
      jwt_issuer: "CLIENTID-4f21ab",
      jwt_subject: "svc-integration",
      jwt_audience: "https://erp.internal/api1/service",
      jwt_token_lifetime: "300s",
      jwt_issued_at_skew: "30s",
      static_headers: { "x-erp-folder": "[REDACTED]" },
    },
    description:
      "ERP connected application. The platform mints a short-lived HS256 assertion per call from the client id and secret the ERP issued.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-19T09:05:00Z",
  },
  {
    // An upstream reached with the RFC 7523 jwt_bearer grant (#1734): the
    // platform signs an assertion with a registered key and exchanges it at the
    // token endpoint. The key comes back redacted, as it does from the real
    // admin API.
    kind: "api",
    name: "acme-ledger-api",
    config: {
      base_url: "https://ledger.example.com/services/data/v61.0",
      auth_mode: "oauth",
      oauth_grant: "jwt_bearer",
      oauth_token_url: "https://login.ledger.example.com/services/oauth2/token",
      oauth_scope: "api",
      jwt_algorithm: "RS256",
      jwt_private_key_pem: "[REDACTED]",
      jwt_key_id: "ledger-2026",
      jwt_issuer: "3MVG9-acme-ledger-consumer",
      jwt_subject: "integration@acme.example.com",
      jwt_audience: "https://login.ledger.example.com",
      jwt_token_lifetime: "180s",
      jwt_issued_at_skew: "30s",
    },
    description:
      "Ledger REST API reached unattended: the platform signs an RS256 assertion with the key the ledger registered and exchanges it for an access token (RFC 7523).",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-18T14:20:00Z",
  },
  {
    // A graphql connection holding a schema an operator uploaded, whose
    // endpoint answers the introspection query with a redirect to a sign-in
    // page. The platform keeps the uploaded schema and records the refusal
    // beside it (#1676); the Schema card has to show both (#1689).
    kind: "graphql",
    name: "acme-orders-graphql",
    config: {
      endpoint_url: "https://orders.internal/graphql",
      auth_mode: "api_key",
      api_key_header: "x-api-key",
      credential: "[REDACTED]",
      schema_validation: "strict",
      max_query_depth: 12,
      namespace_depth: 3,
    },
    description:
      "Order management GraphQL endpoint. Introspection is behind the sign-in redirect, so the schema is the one an operator uploaded.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-21T08:30:00Z",
  },
  {
    // A graphql connection the platform holds no schema for at all: the
    // endpoint refuses introspection and nobody has uploaded one, so
    // graphql_discover has nothing to list.
    kind: "graphql",
    name: "acme-partners-graphql",
    config: {
      endpoint_url: "https://partners.internal/graphql",
      auth_mode: "bearer",
      credential: "[REDACTED]",
      schema_validation: "warn",
    },
    description:
      "Partner portal GraphQL endpoint. Introspection is disabled and no schema has been supplied.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-21T09:05:00Z",
  },
];

// What the platform holds for each graphql connection, keyed by connection
// name: GET /connection-instances/graphql/:name/schema. The two rows are the
// two states the Schema card distinguishes -- a held schema whose last re-read
// failed, and no schema at all.
export const mockGraphQLSchemaState: Record<string, GraphQLSchemaInfo> = {
  "acme-orders-graphql": {
    connection: "acme-orders-graphql",
    schema_hash: "ff68d87b41c2a9e30b5d7c18aa4f6921",
    source: "upload",
    fetched_at: "2025-01-21T08:30:00Z",
    operation_count: 8,
    error:
      "graphql: the endpoint answered HTTP 302 to the introspection query: ",
  },
  "acme-partners-graphql": {
    connection: "acme-partners-graphql",
    operation_count: 0,
    error:
      "graphql: the endpoint refused the introspection query: GraphQL introspection is not allowed",
  },
};

// Bulk OAuth-health rows powering the connection-list health badge. One row per
// connection. Non-gateway kinds report has_oauth=false so the UI hides the
// badge; the three gateway connections are OAuth-configured and healthy
// (token acquired, no re-auth needed, no IdP error).
export const mockConnectionsOAuthHealth: ConnectionsOAuthHealthResponse = {
  connections: mockConnectionInstances.map((c) => {
    const isGateway = c.kind === "mcp" || c.kind === "api";
    return {
      kind: c.kind,
      name: c.name,
      has_oauth: isGateway,
      needs_reauth: false,
      token_acquired: isGateway,
    };
  }),
};

// Per-connection OAuth status snapshots keyed by "kind/name". Only the OAuth
// gateway connections have an entry; the status card hides itself for the rest.
// Tokens are acquired with an expiry comfortably in the future.
const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;
const now = Date.now();

export const mockConnectionOAuthStatus: Record<string, ConnectionOAuthStatus> =
  {
    "mcp/acme-crm-gateway": {
      configured: true,
      token_acquired: true,
      expires_at: new Date(now + 6 * HOUR).toISOString(),
      last_refreshed_at: new Date(now - 42 * 60 * 1000).toISOString(),
      has_refresh_token: true,
      refresh_expires_at: new Date(now + 25 * DAY).toISOString(),
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "crm.read crm.write",
      authenticated_by: "sarah.chen@acme.example.com",
      authenticated_at: new Date(now - 3 * DAY).toISOString(),
      needs_reauth: false,
    },
    "mcp/acme-support-gateway": {
      configured: true,
      token_acquired: true,
      expires_at: new Date(now + 4 * HOUR).toISOString(),
      last_refreshed_at: new Date(now - 15 * 60 * 1000).toISOString(),
      has_refresh_token: true,
      refresh_expires_at: new Date(now + 27 * DAY).toISOString(),
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "support.read",
      authenticated_by: "admin@acme.example.com",
      authenticated_at: new Date(now - 5 * DAY).toISOString(),
      needs_reauth: false,
    },
    "api/acme-billing-api": {
      configured: true,
      token_acquired: true,
      expires_at: new Date(now + 8 * HOUR).toISOString(),
      last_refreshed_at: new Date(now - 8 * 60 * 1000).toISOString(),
      has_refresh_token: true,
      refresh_expires_at: new Date(now + 29 * DAY).toISOString(),
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "billing.read",
      authenticated_by: "admin@acme.example.com",
      authenticated_at: new Date(now - 7 * DAY).toISOString(),
      needs_reauth: false,
      config_vocabulary: "canonical",
    },
    // The connection carrying both vocabularies. It reports configured, which
    // is exactly the trap: the status alone said nothing about the credential
    // it was ignoring.
    "api/acme-analytics-api": {
      configured: true,
      token_acquired: false,
      has_refresh_token: false,
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "analytics.readonly",
      needs_reauth: true,
      config_vocabulary: "mixed",
      shadowed_config_keys: [
        "oauth2_client_id",
        "oauth2_client_secret",
        "oauth2_scopes",
      ],
    },
  };

// Per-connection OAuth-lifecycle timeline keyed by "kind/name". Newest first,
// matching the History panel under the status card. Covers the happy path:
// initial connect, then a series of background refreshes.
export const mockConnectionAuthEvents: Record<string, ConnectionAuthEvent[]> = {
  "mcp/acme-crm-gateway": [
    {
      id: "evt-crm-0005",
      occurred_at: new Date(now - 42 * 60 * 1000).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-crm-gateway",
      event_type: "refresh_succeeded",
      actor: "system:token-refresher",
      idp_host: "auth.acme.example.com",
      detail: { expires_in: 21600 },
    },
    {
      id: "evt-crm-0004",
      occurred_at: new Date(now - 6 * HOUR).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-crm-gateway",
      event_type: "refresh_succeeded",
      actor: "system:token-refresher",
      idp_host: "auth.acme.example.com",
      detail: { expires_in: 21600 },
    },
    {
      id: "evt-crm-0003",
      occurred_at: new Date(now - 1 * DAY).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-crm-gateway",
      event_type: "refresh_succeeded",
      actor: "system:token-refresher",
      idp_host: "auth.acme.example.com",
    },
    {
      id: "evt-crm-0002",
      occurred_at: new Date(now - 3 * DAY + 2000).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-crm-gateway",
      event_type: "connect_completed",
      actor: "sarah.chen@acme.example.com",
      idp_host: "auth.acme.example.com",
      detail: { scope: "crm.read crm.write" },
    },
    {
      id: "evt-crm-0001",
      occurred_at: new Date(now - 3 * DAY).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-crm-gateway",
      event_type: "connect_started",
      actor: "sarah.chen@acme.example.com",
      idp_host: "auth.acme.example.com",
    },
  ],
  "mcp/acme-support-gateway": [
    {
      id: "evt-sup-0003",
      occurred_at: new Date(now - 15 * 60 * 1000).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-support-gateway",
      event_type: "refresh_succeeded",
      actor: "system:token-refresher",
      idp_host: "auth.acme.example.com",
    },
    {
      id: "evt-sup-0002",
      occurred_at: new Date(now - 5 * DAY + 3000).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-support-gateway",
      event_type: "connect_completed",
      actor: "admin@acme.example.com",
      idp_host: "auth.acme.example.com",
      detail: { scope: "support.read" },
    },
    {
      id: "evt-sup-0001",
      occurred_at: new Date(now - 5 * DAY).toISOString(),
      connection_kind: "mcp",
      connection_name: "acme-support-gateway",
      event_type: "connect_started",
      actor: "admin@acme.example.com",
      idp_host: "auth.acme.example.com",
    },
  ],
  "api/acme-billing-api": [
    {
      id: "evt-bil-0003",
      occurred_at: new Date(now - 8 * 60 * 1000).toISOString(),
      connection_kind: "api",
      connection_name: "acme-billing-api",
      event_type: "refresh_succeeded",
      actor: "system:token-refresher",
      idp_host: "auth.acme.example.com",
    },
    {
      id: "evt-bil-0002",
      occurred_at: new Date(now - 7 * DAY + 2500).toISOString(),
      connection_kind: "api",
      connection_name: "acme-billing-api",
      event_type: "connect_completed",
      actor: "admin@acme.example.com",
      idp_host: "auth.acme.example.com",
      detail: { scope: "billing.read" },
    },
    {
      id: "evt-bil-0001",
      occurred_at: new Date(now - 7 * DAY).toISOString(),
      connection_kind: "api",
      connection_name: "acme-billing-api",
      event_type: "connect_started",
      actor: "admin@acme.example.com",
      idp_host: "auth.acme.example.com",
    },
  ],
};

// Runtime reachability for the mcp gateway upstreams, keyed by connection name.
// Same shape the list_connections MCP tool reports so the UI and tool agree.
export const mockGatewayConnectionStatus: Record<
  string,
  GatewayConnectionStatus
> = {
  "acme-crm-gateway": {
    name: "acme-crm-gateway",
    healthy: true,
    auth_mode: "oauth",
    tools: ["crm_search_accounts", "crm_get_account", "crm_list_opportunities"],
    oauth: {
      configured: true,
      token_acquired: true,
      expires_at: new Date(now + 6 * HOUR).toISOString(),
      last_refreshed_at: new Date(now - 42 * 60 * 1000).toISOString(),
      has_refresh_token: true,
      refresh_expires_at: new Date(now + 25 * DAY).toISOString(),
      grant: "authorization_code",
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "crm.read crm.write",
      authenticated_by: "sarah.chen@acme.example.com",
      authenticated_at: new Date(now - 3 * DAY).toISOString(),
      needs_reauth: false,
    },
  },
  "acme-support-gateway": {
    name: "acme-support-gateway",
    healthy: true,
    auth_mode: "oauth",
    tools: [
      "support_search_tickets",
      "support_get_ticket",
      "support_sla_status",
    ],
    oauth: {
      configured: true,
      token_acquired: true,
      expires_at: new Date(now + 4 * HOUR).toISOString(),
      last_refreshed_at: new Date(now - 15 * 60 * 1000).toISOString(),
      has_refresh_token: true,
      refresh_expires_at: new Date(now + 27 * DAY).toISOString(),
      grant: "authorization_code",
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "support.read",
      authenticated_by: "admin@acme.example.com",
      authenticated_at: new Date(now - 5 * DAY).toISOString(),
      needs_reauth: false,
    },
  },
};
