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
    description: "Legacy reporting system key — decommissioned",
    roles: ["viewer"],
    expires_at: daysAgo(15),
    expired: true,
    source: "database",
  },
];

export const mockAPIKeys = {
  keys,
  total: keys.length,
};
