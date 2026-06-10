import type { EnrichmentRule } from "@/api/admin/types";

// Cross-injection enrichment rules attached to the acme-crm-gateway (mcp)
// connection. These demonstrate the platform's headline feature: gateway-proxied
// tool responses are automatically enriched with semantic context from DataHub
// and query availability from Trino. Keyed by connection name to mirror the
// real `/gateway/connections/:connection/enrichment-rules` endpoint.
export const mockEnrichmentRules: Record<string, EnrichmentRule[]> = {
  "acme-crm-gateway": [
    {
      id: "enr-crm-0001",
      connection_name: "acme-crm-gateway",
      tool_name: "crm_search_accounts",
      when_predicate: {
        kind: "response_contains",
        paths: ["accounts[].account_id"],
      },
      enrich_action: {
        source: "acme-catalog",
        operation: "datahub_get_entity",
        parameters: { urn_template: "urn:li:dataset:(crm,accounts,PROD)" },
      },
      merge_strategy: { kind: "path", path: "accounts[].semantic" },
      description:
        "Attach DataHub owners, glossary terms, and deprecation warnings to each returned account.",
      enabled: true,
      created_by: "admin@acme.example.com",
      created_at: "2025-01-08T14:22:00Z",
      updated_at: "2025-01-14T11:05:00Z",
    },
    {
      id: "enr-crm-0002",
      connection_name: "acme-crm-gateway",
      tool_name: "crm_get_account",
      when_predicate: { kind: "always", paths: [] },
      enrich_action: {
        source: "acme-warehouse",
        operation: "trino_describe_table",
        parameters: { table: "crm.public.accounts" },
      },
      merge_strategy: { kind: "path", path: "query_availability" },
      description:
        "Add Trino query availability (row counts, sample SQL) so agents know the account is queryable.",
      enabled: true,
      created_by: "admin@acme.example.com",
      created_at: "2025-01-08T14:25:00Z",
      updated_at: "2025-01-08T14:25:00Z",
    },
    {
      id: "enr-crm-0003",
      connection_name: "acme-crm-gateway",
      tool_name: "crm_list_opportunities",
      when_predicate: {
        kind: "response_contains",
        paths: ["opportunities[].stage"],
      },
      enrich_action: {
        source: "acme-catalog",
        operation: "datahub_get_glossary_term",
        parameters: { term: "SalesStage" },
      },
      merge_strategy: { kind: "path", path: "opportunities[].stage_definition" },
      description:
        "Resolve each opportunity stage to its business glossary definition.",
      enabled: false,
      created_by: "data-engineer@acme.example.com",
      created_at: "2025-01-10T09:14:00Z",
      updated_at: "2025-01-13T16:40:00Z",
    },
  ],
};
