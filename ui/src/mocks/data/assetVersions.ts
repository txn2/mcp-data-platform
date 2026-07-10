import type { AssetVersion } from "@/api/portal/types";

// Version history keyed by asset id. The asset viewer's content view renders
// a version dropdown (one <option> per version, the asset's current_version
// flagged "(current)"); selecting an older version shows its read-only
// content and offers a revert. Empty history is a legitimate state (a
// single-version asset), but the primary demo/screenshot assets carry a
// rich history so the version + revert affordances read as used.
//
// Each list is ordered newest-version-first. Authors and timestamps stay
// consistent with the owning asset in data/assets.ts (alice@example.com and
// collaborators), and the highest `version` matches the asset's
// current_version there.

const iso = (daysAgo: number, hour = 14): string => {
  // Fixed reference date (Jan 2025) so screenshots are deterministic.
  const base = Date.UTC(2025, 0, 9, hour, 22, 0);
  return new Date(base - daysAgo * 86_400_000).toISOString();
};

export const mockAssetVersions: Record<string, AssetVersion[]> = {
  // Q4 Revenue Dashboard (text/html) — five iterations.
  "ast-001": [
    {
      id: "ver-ast-001-5",
      asset_id: "ast-001",
      version: 5,
      s3_key: "assets/ast-001/v5.html",
      s3_bucket: "portal-assets",
      content_type: "text/html",
      size_bytes: 4_250,
      created_by: "alice@example.com",
      change_summary: "Add product-line drilldown and YoY delta column",
      created_at: iso(1),
    },
    {
      id: "ver-ast-001-4",
      asset_id: "ast-001",
      version: 4,
      s3_key: "assets/ast-001/v4.html",
      s3_bucket: "portal-assets",
      content_type: "text/html",
      size_bytes: 3_980,
      created_by: "david.park@example.com",
      change_summary: "Recolor regional bars for WCAG AA contrast",
      created_at: iso(2),
    },
    {
      id: "ver-ast-001-3",
      asset_id: "ast-001",
      version: 3,
      s3_key: "assets/ast-001/v3.html",
      s3_bucket: "portal-assets",
      content_type: "text/html",
      size_bytes: 3_710,
      created_by: "alice@example.com",
      change_summary: "Switch revenue source to sales.quarterly_v2 (returns-adjusted)",
      created_at: iso(2, 9),
    },
    {
      id: "ver-ast-001-2",
      asset_id: "ast-001",
      version: 2,
      s3_key: "assets/ast-001/v2.html",
      s3_bucket: "portal-assets",
      content_type: "text/html",
      size_bytes: 3_240,
      created_by: "alice@example.com",
      change_summary: "Add region filter and quarter selector",
      created_at: iso(3, 16),
    },
    {
      id: "ver-ast-001-1",
      asset_id: "ast-001",
      version: 1,
      s3_key: "assets/ast-001/v1.html",
      s3_bucket: "portal-assets",
      content_type: "text/html",
      size_bytes: 2_900,
      created_by: "alice@example.com",
      change_summary: "Initial dashboard from trino_query + save_artifact",
      created_at: iso(3),
    },
  ],

  // Customer Segmentation Report (markdown) — three iterations.
  "ast-002": [
    {
      id: "ver-ast-002-3",
      asset_id: "ast-002",
      version: 3,
      s3_key: "assets/ast-002/v3.md",
      s3_bucket: "portal-assets",
      content_type: "text/markdown",
      size_bytes: 6_120,
      created_by: "alice@example.com",
      change_summary: "Incorporate churn-risk cohort from ML features",
      created_at: iso(2),
    },
    {
      id: "ver-ast-002-2",
      asset_id: "ast-002",
      version: 2,
      s3_key: "assets/ast-002/v2.md",
      s3_bucket: "portal-assets",
      content_type: "text/markdown",
      size_bytes: 5_400,
      created_by: "marcos.johnson@example.com",
      change_summary: "Add methodology appendix and data-quality caveats",
      created_at: iso(4),
    },
    {
      id: "ver-ast-002-1",
      asset_id: "ast-002",
      version: 1,
      s3_key: "assets/ast-002/v1.md",
      s3_bucket: "portal-assets",
      content_type: "text/markdown",
      size_bytes: 4_800,
      created_by: "alice@example.com",
      change_summary: "First draft of segmentation report",
      created_at: iso(5),
    },
  ],

  // Pipeline Health Runbook (admin-owned asset ast-007) — two iterations.
  "ast-007": [
    {
      id: "ver-ast-007-2",
      asset_id: "ast-007",
      version: 2,
      s3_key: "assets/ast-007/v2.md",
      s3_bucket: "portal-assets",
      content_type: "text/markdown",
      size_bytes: 5_050,
      created_by: "data-platform@acme.example.com",
      change_summary: "Add NiFi backpressure triage section",
      created_at: iso(6),
    },
    {
      id: "ver-ast-007-1",
      asset_id: "ast-007",
      version: 1,
      s3_key: "assets/ast-007/v1.md",
      s3_bucket: "portal-assets",
      content_type: "text/markdown",
      size_bytes: 4_300,
      created_by: "data-platform@acme.example.com",
      change_summary: "Initial runbook",
      created_at: iso(9),
    },
  ],
};

// versionsForAsset returns the history for an asset id (newest first), or an
// empty array when the asset has no recorded history — mirroring the backend
// which returns an empty list for a freshly-created single-version asset.
export function versionsForAsset(id: string): AssetVersion[] {
  return mockAssetVersions[id] ?? [];
}
