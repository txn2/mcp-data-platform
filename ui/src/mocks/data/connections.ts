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
      url: "https://crm-mcp.internal:9000",
      transport: "http",
      auth_mode: "oauth",
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "crm.read crm.write",
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
      url: "https://support-mcp.internal:9100",
      transport: "http",
      auth_mode: "oauth",
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "support.read",
    },
    description:
      "Gateway-proxied support-desk MCP server exposing ticket search and SLA lookups.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-20T10:15:00Z",
  },
  {
    kind: "api",
    name: "acme-billing-api",
    config: {
      base_url: "https://billing.internal/api/v1",
      spec_url: "https://billing.internal/openapi.json",
      auth_mode: "oauth",
      token_url: "https://auth.acme.example.com/oauth2/token",
      scope: "billing.read",
    },
    description:
      "HTTP API gateway over the internal billing service. Exposes invoice and subscription lookups as a single `api_invoke` tool with discovery.",
    created_by: "admin@acme.example.com",
    updated_at: "2025-01-18T14:48:00Z",
  },
];

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

export const mockConnectionOAuthStatus: Record<string, ConnectionOAuthStatus> = {
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
export const mockGatewayConnectionStatus: Record<string, GatewayConnectionStatus> = {
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
    tools: ["support_search_tickets", "support_get_ticket", "support_sla_status"],
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
