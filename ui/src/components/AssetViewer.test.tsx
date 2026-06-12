import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { AssetViewer } from "./AssetViewer";

const stubMutation = () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false, isError: false }) as never;

function markdownAsset(overrides: Record<string, unknown> = {}) {
  return {
    id: "a1",
    owner_id: "owner",
    owner_email: "owner@example.com",
    name: "Notes",
    description: "",
    content_type: "text/markdown",
    s3_bucket: "b",
    s3_key: "k",
    size_bytes: 4,
    tags: [],
    provenance: {},
    current_version: 1,
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-06-01T00:00:00Z",
    ...overrides,
  } as never;
}

function renderViewer(props: Record<string, unknown>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AssetViewer
        asset={markdownAsset()}
        content={"# hi"}
        isLoading={false}
        contentUrl=""
        onBack={() => {}}
        onNavigate={() => {}}
        updateMutation={stubMutation()}
        deleteMutation={stubMutation()}
        {...props}
      />
    </QueryClientProvider>,
  );
}

describe("AssetViewer metadata edit affordance (#611)", () => {
  it("shows the Edit button to a shared editor", () => {
    renderViewer({ isOwner: false, sharePermission: "editor" });
    fireEvent.click(screen.getByTitle("Show details"));
    expect(screen.getByTitle("Edit")).toBeInTheDocument();
  });

  it("hides the Edit button from a shared viewer", () => {
    renderViewer({ isOwner: false, sharePermission: "viewer" });
    fireEvent.click(screen.getByTitle("Show details"));
    expect(screen.queryByTitle("Edit")).not.toBeInTheDocument();
  });

  it("shows the Edit button to the owner", () => {
    renderViewer({ isOwner: true });
    fireEvent.click(screen.getByTitle("Show details"));
    expect(screen.getByTitle("Edit")).toBeInTheDocument();
  });
});
