import { describe, it, expect, afterEach, vi } from "vitest";
import { render } from "@testing-library/react";
import { CollectionSection, ADMIN_ASSET_BASE, PORTAL_ASSET_BASE } from "./CollectionSection";
import { useThemeStore } from "@/stores/theme";

/**
 * A collection tile in dark mode shows the dark capture (#1468).
 *
 * The tile builds its own URL, so the questions the assets grid answers
 * through assetThumbnailSrc -- which variant, and which version -- have to be
 * answered here too. Before #1468 it asked for neither, and every thumbnail in
 * a collection was the light image on a dark page.
 */
function section(overrides: Record<string, unknown> = {}) {
  return {
    id: "sec-1",
    collection_id: "c1",
    title: "Reports",
    description: "",
    position: 0,
    created_at: "2026-01-01T00:00:00Z",
    items: [
      {
        id: "itm-1",
        section_id: "sec-1",
        asset_id: "ast-1",
        position: 0,
        asset_name: "Weekly Inventory",
        asset_content_type: "text/csv",
        asset_description: "",
        asset_thumbnail_s3_key: "k/.thumbnail.png",
        asset_thumbnail_dark_s3_key: "k/.thumbnail_dark.png",
        asset_thumbnail_version: 4,
        asset_thumbnail_dark_version: 4,
        created_at: "2026-01-01T00:00:00Z",
        ...overrides,
      },
    ],
  };
}

function renderSection(assetBase: string, sec = section()) {
  return render(
    <CollectionSection
      section={sec}
      thumbSize="large"
      onOpenItem={vi.fn()}
      assetBase={assetBase}
    />,
  );
}

function tileSrc(container: HTMLElement): string {
  return container.querySelector("img")?.getAttribute("src") ?? "";
}

describe("CollectionSection tile thumbnails (#1468)", () => {
  afterEach(() => {
    useThemeStore.setState({ theme: "light" });
  });

  it("asks for the light capture in light mode", () => {
    useThemeStore.setState({ theme: "light" });
    const { container } = renderSection(PORTAL_ASSET_BASE);
    expect(tileSrc(container)).toBe(`${PORTAL_ASSET_BASE}/ast-1/thumbnail?c=4`);
  });

  it("asks for the dark capture in dark mode", () => {
    useThemeStore.setState({ theme: "dark" });
    const { container } = renderSection(PORTAL_ASSET_BASE);
    expect(tileSrc(container)).toBe(`${PORTAL_ASSET_BASE}/ast-1/thumbnail?variant=dark&c=4`);
  });

  // An administrator reading a collection they do not own gets its tiles from
  // the admin route (#1292), and reads it in a color mode of their own.
  it("asks the admin route for the dark capture too", () => {
    useThemeStore.setState({ theme: "dark" });
    const { container } = renderSection(ADMIN_ASSET_BASE);
    expect(tileSrc(container)).toBe(`${ADMIN_ASSET_BASE}/ast-1/thumbnail?variant=dark&c=4`);
  });

  // HTML, JSX and SVG carry their own colors: one capture serves both modes,
  // so an empty dark key is not a gap to fall back from.
  it("keeps the only capture for a content type that stores one", () => {
    useThemeStore.setState({ theme: "dark" });
    const { container } = renderSection(
      PORTAL_ASSET_BASE,
      section({
        asset_content_type: "text/html",
        asset_thumbnail_dark_s3_key: "",
        asset_thumbnail_dark_version: 0,
      }),
    );
    expect(tileSrc(container)).toBe(`${PORTAL_ASSET_BASE}/ast-1/thumbnail?c=4`);
  });

  it("shows no image at all for an asset that has never been captured", () => {
    const { container } = renderSection(
      PORTAL_ASSET_BASE,
      section({ asset_thumbnail_s3_key: "", asset_thumbnail_version: 0 }),
    );
    expect(container.querySelector("img")).toBeNull();
  });
});
