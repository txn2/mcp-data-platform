import { describe, it, expect, afterEach, vi } from "vitest";
import { render } from "@testing-library/react";
import type { Asset } from "@/api/portal/types";
import { AssetBrowserModal } from "./AssetBrowserModal";
import { useThemeStore } from "@/stores/theme";

/**
 * The picker an editor adds assets from shows the same thumbnails the
 * collection will, so it answers the same question about color mode (#1468).
 */
function asset(over: Partial<Asset> = {}): Asset {
  return {
    id: "ast-1",
    owner_id: "u1",
    owner_email: "owner@example.com",
    name: "Weekly Inventory",
    description: "",
    content_type: "text/csv",
    s3_bucket: "b",
    s3_key: "k/content.csv",
    thumbnail_s3_key: "k/.thumbnail.png",
    thumbnail_dark_s3_key: "k/.thumbnail_dark.png",
    thumbnail_version: 4,
    thumbnail_dark_version: 4,
    size_bytes: 10,
    tags: [],
    provenance: {},
    session_id: "",
    current_version: 4,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

function tileSrc(container: HTMLElement): string {
  return container.querySelector("img")?.getAttribute("src") ?? "";
}

describe("AssetBrowserModal thumbnails (#1468)", () => {
  afterEach(() => {
    useThemeStore.setState({ theme: "light" });
  });

  it("asks for the light capture in light mode", () => {
    useThemeStore.setState({ theme: "light" });
    const { container } = render(
      <AssetBrowserModal assets={[asset()]} onAdd={vi.fn()} onClose={vi.fn()} />,
    );
    expect(tileSrc(container)).toBe("/api/v1/portal/assets/ast-1/thumbnail?c=4");
  });

  it("asks for the dark capture in dark mode", () => {
    useThemeStore.setState({ theme: "dark" });
    const { container } = render(
      <AssetBrowserModal assets={[asset()]} onAdd={vi.fn()} onClose={vi.fn()} />,
    );
    expect(tileSrc(container)).toBe("/api/v1/portal/assets/ast-1/thumbnail?variant=dark&c=4");
  });
});
