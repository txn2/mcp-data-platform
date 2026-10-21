import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";

const { asset, versionsCalls } = vi.hoisted(() => ({
  asset: { value: {} as Record<string, unknown> },
  versionsCalls: [] as boolean[],
}));

vi.mock("@/api/portal/hooks", () => {
  const mutation = { mutate: vi.fn(), isPending: false };
  return {
    useAsset: () => ({ data: asset.value, isLoading: false }),
    useAssetContent: () => ({ data: "# x", error: null, refetch: vi.fn() }),
    useUpdateAsset: () => mutation,
    useDeleteAsset: () => mutation,
    useUpdateAssetContent: () => mutation,
    useCopyAsset: () => mutation,
    useRevertVersion: () => mutation,
    useVersionContent: () => ({ data: undefined, isLoading: false }),
    useAssetVersions: (_id: string, enabled: boolean) => {
      versionsCalls.push(enabled);
      return { data: enabled ? { data: [{ version: 1 }, { version: 2 }] } : undefined, isLoading: false };
    },
  };
});

// The viewer itself is not under test: only what the page hands it.
const { received } = vi.hoisted(() => ({ received: { versions: undefined as unknown } }));
vi.mock("@/components/AssetViewer", () => ({
  AssetViewer: (props: { versions?: unknown }) => {
    received.versions = props.versions;
    return null;
  },
}));
vi.mock("@/components/feedback/FeedbackButton", () => ({ FeedbackButton: () => null }));
vi.mock("@/components/share/SharedPageLink", () => ({ SharedPageLink: () => null }));

import { AssetViewerPage } from "./AssetViewerPage";
import { useAuthStore } from "@/stores/auth";

function renderPage() {
  render(<AssetViewerPage assetId="a1" onNavigate={vi.fn()} onBack={vi.fn()} />);
}

describe("AssetViewerPage history (#2226)", () => {
  beforeEach(() => {
    versionsCalls.length = 0;
    received.versions = undefined;
    useAuthStore.setState({ user: { user_id: "u", roles: [], is_admin: false } });
  });

  it("asks a viewer for no history and hands the viewer none", () => {
    asset.value = { id: "a1", is_owner: false, share_permission: "viewer", current_version: 3 };
    renderPage();
    expect(versionsCalls.every((e) => e === false)).toBe(true);
    expect(received.versions).toBeUndefined();
  });

  it("gives the owner and an editor the history", () => {
    asset.value = { id: "a1", is_owner: true, current_version: 3 };
    renderPage();
    expect(received.versions).toHaveLength(2);
    asset.value = { id: "a1", is_owner: false, share_permission: "editor", current_version: 3 };
    renderPage();
    expect(received.versions).toHaveLength(2);
  });

  it("gives an administrator the history of an asset shared to them as viewer", () => {
    useAuthStore.setState({ user: { user_id: "u", roles: [], is_admin: true } });
    asset.value = { id: "a1", is_owner: false, share_permission: "viewer", current_version: 3 };
    renderPage();
    expect(received.versions).toHaveLength(2);
  });
});
