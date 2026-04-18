interface ChangelogEntry {
  version: string;
  date: string;
  changes: {
    type: "added" | "changed" | "fixed" | "removed";
    description: string;
  }[];
}

const now = new Date();
function daysAgo(n: number): string {
  const d = new Date(now);
  d.setDate(d.getDate() - n);
  return d.toISOString().slice(0, 10);
}

export const mockChangelog: ChangelogEntry[] = [
  {
    version: "1.4.2",
    date: daysAgo(3),
    changes: [
      {
        type: "fixed",
        description:
          "Resolved race condition in concurrent tool calls that could return stale audit timestamps",
      },
      {
        type: "fixed",
        description:
          "Corrected memory recall scoring when multiple dimensions match the same entity URN",
      },
      {
        type: "changed",
        description:
          "Improved Trino query explain output formatting for nested joins",
      },
    ],
  },
  {
    version: "1.4.1",
    date: daysAgo(12),
    changes: [
      {
        type: "fixed",
        description:
          "Persona tool filtering now correctly applies deny rules before allow rules",
      },
      {
        type: "fixed",
        description:
          "Config changelog entries no longer duplicate on rapid consecutive updates",
      },
    ],
  },
  {
    version: "1.4.0",
    date: daysAgo(25),
    changes: [
      {
        type: "added",
        description:
          "Memory system for persistent knowledge capture across sessions with dimension-based organization",
      },
      {
        type: "added",
        description:
          "Resource management with scoped file storage at global, persona, and user levels",
      },
      {
        type: "changed",
        description:
          "Enrichment pipeline now incorporates memory recall alongside knowledge base lookups",
      },
      {
        type: "changed",
        description:
          "Admin portal config page shows effective configuration with file vs database source indicators",
      },
    ],
  },
  {
    version: "1.3.0",
    date: daysAgo(50),
    changes: [
      {
        type: "added",
        description:
          "Prompt management with global, persona, and personal scopes",
      },
      {
        type: "added",
        description:
          "Database-managed connections alongside file-based configuration",
      },
      {
        type: "changed",
        description:
          "API key management supports database-backed keys with expiration dates",
      },
    ],
  },
  {
    version: "1.2.0",
    date: daysAgo(80),
    changes: [
      {
        type: "added",
        description:
          "Knowledge system with automated insight capture and changeset tracking",
      },
      {
        type: "added",
        description:
          "Audit analytics dashboard with timeseries, breakdown, and performance views",
      },
      {
        type: "changed",
        description:
          "Tool schemas now include title field for improved display in client UIs",
      },
      {
        type: "removed",
        description:
          "Deprecated v0 audit event format — all consumers must use the v1 schema",
      },
    ],
  },
  {
    version: "1.1.0",
    date: daysAgo(120),
    changes: [
      {
        type: "added",
        description:
          "Persona system for role-based tool access and context customization",
      },
      {
        type: "added",
        description:
          "Enrichment pipeline for automatic query context augmentation from DataHub metadata",
      },
      {
        type: "changed",
        description:
          "Audit events now capture enrichment status and content block counts",
      },
    ],
  },
  {
    version: "1.0.1",
    date: daysAgo(150),
    changes: [
      {
        type: "fixed",
        description:
          "S3 presigned URL generation now correctly handles keys with special characters",
      },
      {
        type: "fixed",
        description:
          "Trino connection pooling releases idle connections after the configured timeout",
      },
    ],
  },
  {
    version: "1.0.0",
    date: daysAgo(180),
    changes: [
      {
        type: "added",
        description:
          "Initial release with Trino query, DataHub metadata, and S3 object storage tools",
      },
      {
        type: "added",
        description:
          "Admin portal with system overview, tool browser, and audit event log",
      },
      {
        type: "added",
        description:
          "User portal with conversation-scoped artifact management and sharing",
      },
    ],
  },
];
