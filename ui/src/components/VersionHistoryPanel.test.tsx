import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { VersionHistoryPanel } from "./VersionHistoryPanel";
import type { AssetVersion } from "@/api/portal/types";

afterEach(cleanup);

function version(n: number, metadata?: Record<string, unknown>): AssetVersion {
  return {
    id: `v${n}`,
    asset_id: "a1",
    version: n,
    s3_key: `k${n}`,
    s3_bucket: "b",
    content_type: "text/csv",
    size_bytes: 1024,
    created_by: "alice@example.com",
    change_summary: "Exported from Trino query",
    created_at: "2026-10-08T10:00:00Z",
    metadata,
  };
}

describe("VersionHistoryPanel", () => {
  it("marks the versions an export cut at a limit (#2057)", () => {
    render(
      <VersionHistoryPanel
        versions={[version(2, { truncated: true, limit_applied: 250, limit_unit: "pages" }), version(1)]}
        currentVersion={2}
        isLoading={false}
      />,
    );
    expect(screen.getByTestId("version-incomplete-2")).toHaveTextContent("Incomplete: truncated at 250 pages");
    expect(screen.queryByTestId("version-incomplete-1")).not.toBeInTheDocument();
  });
});
