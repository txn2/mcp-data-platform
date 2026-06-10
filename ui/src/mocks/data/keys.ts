import type { APIKeySummary } from "@/api/admin/types";

const now = new Date();
function daysAgo(n: number): string {
  const d = new Date(now);
  d.setDate(d.getDate() - n);
  return d.toISOString();
}

function daysFromNow(n: number): string {
  const d = new Date(now);
  d.setDate(d.getDate() + n);
  return d.toISOString();
}

const keys: APIKeySummary[] = [
  {
    name: "sarah-admin",
    description: "Primary admin key for platform management",
    roles: ["admin"],
    source: "file",
  },
  {
    name: "ci-pipeline",
    email: "ci@example.com",
    description: "CI/CD pipeline integration for automated schema validation",
    roles: ["admin"],
    expires_at: daysFromNow(90),
    source: "database",
  },
  {
    name: "analyst-readonly",
    email: "analytics-team@example.com",
    description: "Read-only access for the analytics team dashboards",
    roles: ["viewer"],
    expires_at: daysFromNow(30),
    source: "database",
  },
  {
    name: "claude-integration",
    description: "Claude.ai MCP integration",
    roles: ["admin"],
    source: "file",
  },
  {
    name: "expired-legacy-key",
    email: "legacy-system@example.com",
    description: "Legacy reporting system key - decommissioned",
    roles: ["viewer"],
    expires_at: daysAgo(15),
    expired: true,
    source: "database",
  },
  {
    name: "data-engineering-team",
    email: "data-eng@example.com",
    description: "Shared key for the data engineering team's ETL tooling",
    roles: ["data_engineer"],
    expires_at: daysFromNow(180),
    source: "database",
  },
  {
    name: "grafana-metrics",
    email: "observability@example.com",
    description: "Grafana service account scraping the observability endpoints",
    roles: ["viewer"],
    source: "file",
  },
  {
    name: "airflow-orchestrator",
    email: "airflow@example.com",
    description: "Airflow DAGs invoking trino_export and save_artifact",
    roles: ["data_engineer"],
    expires_at: daysFromNow(365),
    source: "database",
  },
  {
    name: "regional-director-bi",
    email: "regional-bi@example.com",
    description: "Regional director BI dashboards (read-only catalog access)",
    roles: ["regional_director"],
    expires_at: daysFromNow(7),
    source: "database",
  },
  {
    name: "finance-reporting",
    email: "finance@example.com",
    description: "Finance executive monthly reporting integration",
    roles: ["finance_executive"],
    expires_at: daysFromNow(45),
    source: "database",
  },
  {
    name: "store-ops-mobile",
    email: "store-ops@example.com",
    description: "Store manager mobile app gateway key",
    roles: ["store_manager"],
    expires_at: daysFromNow(120),
    source: "database",
  },
  {
    name: "security-audit-rotating",
    email: "secops@example.com",
    description: "Quarterly security audit access, rotated each cycle",
    roles: ["admin"],
    expires_at: daysFromNow(3),
    source: "database",
  },
  {
    name: "partner-integration-acme",
    email: "partner-api@example.com",
    description: "Third-party partner integration (scoped, expiring soon)",
    roles: ["viewer"],
    expires_at: daysFromNow(2),
    source: "database",
  },
  {
    name: "expired-contractor",
    email: "contractor@example.com",
    description: "Contractor access from Q3 engagement - expired",
    roles: ["data_engineer"],
    expires_at: daysAgo(60),
    expired: true,
    source: "database",
  },
  {
    name: "local-dev",
    description: "Developer laptop key for local platform testing",
    roles: ["admin"],
    source: "both",
  },
];

export const mockAPIKeys = {
  keys,
  total: keys.length,
};
