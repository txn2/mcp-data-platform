import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Asset } from "@/api/portal/types";
import { AssetMetadataSidebar } from "./AssetMetadataSidebar";
import { shortSessionId } from "@/pages/sessions/kind";

// An asset records the session that made it, and until #1319 that id was text
// with nowhere to go. What is asserted here is the walk: the sidebar and the
// provenance panel both lead to the session, and neither offers the walk to a
// reader the session would refuse.

const SESSION_ID = "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b";

function asset(overrides: Partial<Asset> = {}): Asset {
  return {
    id: "ast-001",
    owner_id: "user-alice",
    owner_email: "alice@example.com",
    name: "Q3 revenue by region",
    description: "",
    content_type: "text/csv",
    s3_bucket: "portal-assets",
    s3_key: "assets/ast-001.csv",
    size_bytes: 1024,
    tags: [],
    provenance: {
      session_id: SESSION_ID,
      user_id: "user-alice",
      tool_calls: [
        {
          tool_name: "trino_query",
          timestamp: "2026-08-16T10:00:00Z",
          parameters: { sql: "SELECT 1" },
        },
      ],
    },
    session_id: SESSION_ID,
    current_version: 1,
    created_at: "2026-08-16T10:00:00Z",
    updated_at: "2026-08-16T10:00:00Z",
    ...overrides,
  } as Asset;
}

function renderSidebar(
  props: Partial<Parameters<typeof AssetMetadataSidebar>[0]> = {},
) {
  const onNavigate = vi.fn();
  // The sidebar queries what tables are registered over the asset (#1327), so
  // it needs a client even in a test about the session walk.
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AssetMetadataSidebar
        asset={asset()}
        editing={false}
        editName=""
        editDesc=""
        editTags=""
        editRetentionMode="default"
        editRetentionCustom=""
        canSetRetention
        onEditNameChange={vi.fn()}
        onEditDescChange={vi.fn()}
        onEditTagsChange={vi.fn()}
        onEditRetentionModeChange={vi.fn()}
        onEditRetentionCustomChange={vi.fn()}
        onStartEdit={vi.fn()}
        onSaveEdit={vi.fn()}
        onCancelEdit={vi.fn()}
        updateMutation={{ isPending: false, mutate: vi.fn() }}
        isOwner
        isSharedEditor={false}
        onNavigate={onNavigate}
        sessionPath={(id) => `/activity/sessions/${id}`}
        {...props}
      />
    </QueryClientProvider>,
  );
  return { onNavigate };
}

afterEach(cleanup);

describe("walking from an asset to the session that made it", () => {
  it("links the session id in the details list", () => {
    const { onNavigate } = renderSidebar();

    fireEvent.click(screen.getByText(shortSessionId(SESSION_ID)));
    expect(onNavigate).toHaveBeenCalledWith(`/activity/sessions/${SESSION_ID}`);
  });

  // The panel shows the calls captured when the asset was saved; the session
  // holds every call that session made, which is the reason to walk there.
  it("offers the session from the provenance panel", () => {
    const { onNavigate } = renderSidebar();

    fireEvent.click(screen.getByText("Open session"));
    expect(onNavigate).toHaveBeenCalledWith(`/activity/sessions/${SESSION_ID}`);
  });

  // A session refuses everyone but its own caller and an admin, so on someone
  // else's shared asset the link would lead to a not-found.
  it("offers no session where the reader could not open it", () => {
    renderSidebar({ sessionPath: undefined });

    expect(screen.queryByText(shortSessionId(SESSION_ID))).toBeNull();
    expect(screen.queryByText("Open session")).toBeNull();
  });

  // An asset saved before provenance was captured still came from a session,
  // and that session is where its calls are recorded.
  it("still offers the session when no calls were captured", () => {
    const { onNavigate } = renderSidebar({
      asset: asset({ provenance: { session_id: SESSION_ID, tool_calls: [] } }),
    });

    expect(screen.getByText("No provenance data available.")).toBeTruthy();
    fireEvent.click(screen.getByText("Open session"));
    expect(onNavigate).toHaveBeenCalledWith(`/activity/sessions/${SESSION_ID}`);
  });
});

// #2057: an asset whose current version an export cut says so at the top of
// its sidebar, with the limit its version records.
describe("an incomplete export", () => {
  it("states the cut the current version records", () => {
    renderSidebar({
      asset: asset({ tags: ["_sys-truncated"], current_version: 2 }),
      versions: [
        {
          id: "v2", asset_id: "ast-001", version: 2, s3_key: "k2", s3_bucket: "b", content_type: "text/csv",
          size_bytes: 10, created_by: "alice@example.com", change_summary: "", created_at: "2026-08-16T10:00:00Z",
          metadata: { truncated: true, limit_applied: 100000, limit_unit: "rows", limit_source: "deployment" },
        },
      ],
    });
    expect(screen.getByTestId("incomplete-notice")).toHaveTextContent("Incomplete: truncated at 100,000 rows");
  });

  it("says nothing about a complete asset", () => {
    renderSidebar();
    expect(screen.queryByTestId("incomplete-notice")).toBeNull();
  });
});
