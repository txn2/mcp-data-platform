import type {
  ConfigEntry,
  ConfigChangelogEntry,
  EffectiveConfigEntry,
} from "@/api/admin/types";

const now = new Date();
function daysAgo(n: number): string {
  const d = new Date(now);
  d.setDate(d.getDate() - n);
  return d.toISOString();
}
function hoursAgo(n: number): string {
  const d = new Date(now);
  d.setHours(d.getHours() - n);
  return d.toISOString();
}

export const mockEffectiveConfig: EffectiveConfigEntry[] = [
  {
    key: "server.name",
    value: "acme-retail-platform",
    source: "file",
  },
  {
    key: "server.description",
    value:
      "ACME Corp Retail Data Platform provides unified access to sales, inventory, and supply chain data across 200+ store locations. It connects analysts, engineers, and regional directors to governed data through natural language queries and automated enrichment. The platform serves as the single source of truth for all retail analytics and operational reporting.",
    source: "database",
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(3),
  },
  {
    key: "server.agent_instructions",
    value: [
      "You are an AI assistant for ACME Corp's retail data platform.",
      "Always use the retail schema for sales and transaction queries.",
      "Use the inventory schema for stock levels, reorder points, and warehouse data.",
      "When querying daily_sales, always include a date range filter to avoid full table scans.",
      "Store IDs follow the format STR-XXXX where XXXX is a four-digit regional code.",
      "Revenue figures are stored in cents — divide by 100 for dollar amounts.",
      "The fiscal year starts February 1st. Use fiscal_quarter from the analytics schema for period comparisons.",
      "Never expose raw customer PII. Use the customer_segments table for aggregated demographic analysis.",
    ].join("\n"),
    source: "database",
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(5),
  },
  {
    key: "server.tags",
    value: "retail,inventory,analytics,acme-corp",
    source: "database",
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(14),
  },
  {
    key: "audit.enabled",
    value: "true",
    source: "file",
  },
  {
    key: "knowledge.enabled",
    value: "true",
    source: "file",
  },
  {
    key: "portal.title",
    value: "ACME Corp Data Platform",
    source: "database",
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(20),
  },
];

export const mockConfigEntries: ConfigEntry[] = [
  {
    key: "server.description",
    value:
      "ACME Corp Retail Data Platform provides unified access to sales, inventory, and supply chain data across 200+ store locations. It connects analysts, engineers, and regional directors to governed data through natural language queries and automated enrichment. The platform serves as the single source of truth for all retail analytics and operational reporting.",
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(3),
  },
  {
    key: "server.agent_instructions",
    value: [
      "You are an AI assistant for ACME Corp's retail data platform.",
      "Always use the retail schema for sales and transaction queries.",
      "Use the inventory schema for stock levels, reorder points, and warehouse data.",
      "When querying daily_sales, always include a date range filter to avoid full table scans.",
      "Store IDs follow the format STR-XXXX where XXXX is a four-digit regional code.",
      "Revenue figures are stored in cents — divide by 100 for dollar amounts.",
      "The fiscal year starts February 1st. Use fiscal_quarter from the analytics schema for period comparisons.",
      "Never expose raw customer PII. Use the customer_segments table for aggregated demographic analysis.",
    ].join("\n"),
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(5),
  },
  {
    key: "server.tags",
    value: "retail,inventory,analytics,acme-corp",
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(14),
  },
  {
    key: "portal.title",
    value: "ACME Corp Data Platform",
    updated_by: "sarah.chen@example.com",
    updated_at: daysAgo(20),
  },
];

export const mockConfigChangelog: ConfigChangelogEntry[] = [
  {
    id: 15,
    key: "server.description",
    action: "update",
    value:
      "ACME Corp Retail Data Platform provides unified access to sales, inventory, and supply chain data across 200+ store locations. It connects analysts, engineers, and regional directors to governed data through natural language queries and automated enrichment. The platform serves as the single source of truth for all retail analytics and operational reporting.",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(3),
  },
  {
    id: 14,
    key: "server.agent_instructions",
    action: "update",
    value: "Updated agent instructions with fiscal year guidance",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(5),
  },
  {
    id: 13,
    key: "server.agent_instructions",
    action: "update",
    value: "Added customer PII handling instruction",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(7),
  },
  {
    id: 12,
    key: "server.description",
    action: "update",
    value: "Expanded description to mention 200+ store locations",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(8),
  },
  {
    id: 11,
    key: "server.tags",
    action: "update",
    value: "retail,inventory,analytics,acme-corp",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(14),
  },
  {
    id: 10,
    key: "server.tags",
    action: "update",
    value: "retail,inventory,analytics",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(15),
  },
  {
    id: 9,
    key: "portal.title",
    action: "update",
    value: "ACME Corp Data Platform",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(20),
  },
  {
    id: 8,
    key: "portal.title",
    action: "create",
    value: "ACME Retail Platform",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(21),
  },
  {
    id: 7,
    key: "server.agent_instructions",
    action: "update",
    value: "Added store ID format documentation",
    changed_by: "marcus.johnson@example.com",
    changed_at: daysAgo(22),
  },
  {
    id: 6,
    key: "server.agent_instructions",
    action: "update",
    value: "Added revenue cents conversion note",
    changed_by: "marcus.johnson@example.com",
    changed_at: daysAgo(23),
  },
  {
    id: 5,
    key: "server.description",
    action: "update",
    value: "Added enrichment mention to description",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(24),
  },
  {
    id: 4,
    key: "server.agent_instructions",
    action: "create",
    value: "Initial agent instructions for retail platform",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(25),
  },
  {
    id: 3,
    key: "server.description",
    action: "create",
    value: "Initial platform description",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(27),
  },
  {
    id: 2,
    key: "server.tags",
    action: "create",
    value: "retail,inventory",
    changed_by: "sarah.chen@example.com",
    changed_at: daysAgo(28),
  },
  {
    id: 1,
    key: "knowledge.enabled",
    action: "delete",
    changed_by: "sarah.chen@example.com",
    changed_at: hoursAgo(2),
  },
];
