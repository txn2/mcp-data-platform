import type {
  Application,
  ApplicationDetail,
  ApplicationRelease,
} from "@/api/portal/hooks/applications";

// Two applications (#2411): "shop", released twice with a rollback between,
// made of a schema, two scripts, an asset and a webhook source; and "intake",
// recorded but never released. The script, asset and source ids are ones the
// other mocks serve, so a part reads as something that exists.

export const mockApplications: Application[] = [
  {
    id: "app-shop",
    name: "shop",
    description: "The storefront, its order API and the daily sales report.",
    owner_email: "sarah.chen@example.com",
    connection: "acme-shop",
    schema: "shop",
    current_release: 3,
    created_by: "sarah.chen@example.com",
    created_at: "2025-01-10T09:00:00Z",
    updated_at: "2025-01-22T16:30:00Z",
    parts: [
      { kind: "script", id: "script-001", added_by: "sarah.chen@example.com", added_at: "2025-01-10T09:05:00Z" },
      { kind: "script", id: "script-002", added_by: "sarah.chen@example.com", added_at: "2025-01-10T09:06:00Z" },
      { kind: "asset", id: "ast-001", added_by: "sarah.chen@example.com", added_at: "2025-01-12T11:00:00Z" },
      { kind: "source", id: "billing-events", added_by: "admin@example.com", added_at: "2025-01-15T14:00:00Z" },
    ],
  },
  {
    id: "app-intake",
    name: "intake",
    description: "The customer intake form and where its submissions are filed.",
    owner_email: "j.martinez@example.com",
    connection: "",
    schema: "",
    current_release: 0,
    created_by: "j.martinez@example.com",
    created_at: "2025-01-20T10:00:00Z",
    updated_at: "2025-01-20T10:00:00Z",
    parts: [
      { kind: "script", id: "script-004", added_by: "j.martinez@example.com", added_at: "2025-01-20T10:02:00Z" },
    ],
  },
];

const shopReleases: ApplicationRelease[] = [
  {
    number: 3,
    action: "rollback",
    migration_version: 2,
    restored: 1,
    changes: {
      parts: [{ kind: "script", part_id: "script-001", from: 5, to: 4 }],
      applied: [],
      reverted: [],
      migrations_kept: "Migration 3 stays applied: a rollback does not revert the schema.",
    },
    note: "The new report total was wrong.",
    created_by: "admin@example.com",
    created_at: "2025-01-22T16:30:00Z",
    pins: [
      { kind: "script", part_id: "script-001", version: 4 },
      { kind: "script", part_id: "script-002", version: 2 },
      { kind: "asset", part_id: "ast-001", version: 3 },
    ],
  },
  {
    number: 2,
    action: "release",
    migration_version: 3,
    changes: {
      parts: [
        { kind: "script", part_id: "script-001", from: 4, to: 5 },
        { kind: "asset", part_id: "ast-001", from: 2, to: 3 },
      ],
      applied: [3],
      reverted: [],
    },
    note: "Adds the refunds column to the report.",
    created_by: "admin@example.com",
    created_at: "2025-01-21T12:00:00Z",
    pins: [
      { kind: "script", part_id: "script-001", version: 5 },
      { kind: "script", part_id: "script-002", version: 2 },
      { kind: "asset", part_id: "ast-001", version: 3 },
    ],
  },
  {
    number: 1,
    action: "release",
    migration_version: 2,
    changes: {
      parts: [
        { kind: "script", part_id: "script-001", from: 0, to: 4 },
        { kind: "script", part_id: "script-002", from: 0, to: 2 },
        { kind: "asset", part_id: "ast-001", from: 0, to: 2 },
      ],
      applied: [1, 2],
      reverted: [],
    },
    created_by: "admin@example.com",
    created_at: "2025-01-15T15:00:00Z",
    pins: [
      { kind: "script", part_id: "script-001", version: 4 },
      { kind: "script", part_id: "script-002", version: 2 },
      { kind: "asset", part_id: "ast-001", version: 2 },
    ],
  },
];

export const mockApplicationDetails: Record<string, ApplicationDetail> = {
  "app-shop": {
    application: mockApplications[0]!,
    current_release: shopReleases[0]!,
    pending: [{ kind: "script", part_id: "script-001", from: 4, to: 5 }],
    migrations: { applied: 3, pending: [4] },
    problems: [],
    releases: shopReleases,
  },
  "app-intake": {
    application: mockApplications[1]!,
    current_release: null,
    pending: [{ kind: "script", part_id: "script-004", from: 0, to: 1 }],
    problems: ["script-004 has no saved version that passed its tests"],
    releases: [],
  },
};

/** A bundle as the shop export writes it, for the import dialog's tests. */
export const mockApplicationBundle = {
  format: "mcp-data-platform/application",
  format_version: 1,
  application: { name: "shop", description: "The storefront, its order API and the daily sales report." },
  release: 2,
  exported_at: "2025-01-21T12:05:00Z",
  scripts: [],
  assets: [],
  sources: ["billing-events"],
  names: {
    connections: ["acme-shop"],
    secrets: ["stripe-key"],
    sources: ["billing-events"],
    bot_checks: [],
  },
};
