import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Asset } from "@/api/portal/types";
import type { Resource } from "@/api/resources/types";
import { assetSubject, resourceSubject } from "@/lib/thumbnailSupport";
import { ThumbnailPanel } from "./ThumbnailPanel";

// A person looking at a tile that shows the artifact's error state had nothing
// to press: the capture is taken in a browser, the refresh queue offers only
// assets whose row says a capture is missing or behind, and the only way to
// move that row was to write a new version -- which captured it wrong again
// under the same policy (#1497). What is asserted here is the way back.

const ASSET: Asset = {
  id: "ast-q4",
  owner_id: "u1",
  owner_email: "alex.rivera@example.com",
  name: "Q4 dashboard",
  description: "",
  content_type: "text/jsx",
  s3_bucket: "b",
  s3_key: "portal/u1/ast-q4/content.jsx",
  thumbnail_s3_key: "portal/u1/ast-q4/.thumbnail.png",
  thumbnail_version: 4,
  thumbnail_dark_version: 0,
  size_bytes: 2048,
  tags: [],
  provenance: {},
  current_version: 4,
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
} as unknown as Asset;

let calls: { url: string; method: string; cache?: string }[] = [];

function stubApi(status = 200) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), method: init?.method ?? "GET", cache: init?.cache });
      return Promise.resolve(new Response(JSON.stringify({ status: "updated" }), { status }));
    }),
  );
}

/** The row between a clear landing and the replacement capture being stored. */
const CLEARED: Asset = { ...ASSET, thumbnail_s3_key: "", thumbnail_version: 0 };

function renderPanel(asset: Asset = ASSET, isOwner = true, assetApiBase?: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = (a: Asset) => (
    <QueryClientProvider client={qc}>
      <ThumbnailPanel subject={assetSubject(a, assetApiBase)} canModify={isOwner} />
    </QueryClientProvider>
  );
  const result = render(tree(asset));
  // Rerenders the same panel against a later state of the asset row, which is
  // how the clear and the capture that follows it reach this component.
  return { ...result, rerender: (a: Asset) => result.rerender(tree(a)) };
}

// The same panel over a managed resource. A resource is captured by the same
// capturer and stored under the same rule, and had neither the picture nor the
// button until #1568; what differs is that it carries no version, so its
// captures are dated against the file's own updated_at.
const RESOURCE: Resource = {
  id: "res-notes",
  scope: "user",
  scope_id: "u1",
  path: "notes",
  filename: "notes.md",
  display_name: "Release notes",
  description: "",
  mime_type: "text/markdown",
  size_bytes: 900,
  s3_key: "user/u1/notes/notes.md",
  uri: "mcp://user/u1/notes/notes.md",
  tags: [],
  uploader_sub: "u1",
  uploader_email: "alex.rivera@example.com",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-02T00:00:00Z",
  thumbnail_s3_key: "user/u1/notes/.thumbnail.png",
  thumbnail_dark_s3_key: "user/u1/notes/.thumbnail_dark.png",
  thumbnail_captured_at: "2026-08-02T00:00:00Z",
  thumbnail_dark_captured_at: "2026-08-02T00:00:00Z",
};

function renderResourcePanel(resource: Resource = RESOURCE, canModify = true) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ThumbnailPanel subject={resourceSubject(resource)} canModify={canModify} />
    </QueryClientProvider>,
  );
}

/** The panel as a surface that reads tiles through a route of its own. */
function renderPanelWithBase(base: string) {
  return renderPanel(ASSET, true, base);
}

describe("ThumbnailPanel", () => {
  beforeEach(() => stubApi());
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("shows the stored tile so the owner can see what everyone else sees", () => {
    renderPanel();
    const img = screen.getByAltText("Thumbnail for Q4 dashboard");
    expect(img.getAttribute("src")).toContain("/api/v1/portal/assets/ast-q4/thumbnail");
  });

  it("discards the stored image on request, which is what re-queues the asset", async () => {
    renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));

    await waitFor(() => expect(calls[0]).toBeDefined());
    expect(calls[0]).toMatchObject({
      url: "/api/v1/portal/assets/ast-q4/thumbnail",
      method: "DELETE",
    });
  });

  // The replacement is stored at the same asset version, so the tile's URL is
  // the one this browser already holds a cached copy of and the route is
  // cacheable for an hour: without replacing that entry the person who pressed
  // the button keeps seeing the picture they asked to replace (#1497).
  it("replaces this browser's cached copy once the new capture has landed", async () => {
    const { rerender } = renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));
    await waitFor(() => expect(calls).toHaveLength(1));

    // The cleared row arrives on the invalidation the clear issues. Until it
    // does, the panel is still holding the image the reader asked to be rid of,
    // and replacing the cache entry for THAT is both useless and destructive:
    // it spends the one trigger there is, and points the panel at a
    // cache-busted URL of a row the server has already cleared (#1501).
    rerender(CLEARED);
    await new Promise((r) => setTimeout(r, 20));
    expect(calls).toHaveLength(1);

    // The replacement lands: the row names a capture again, at the version the
    // reader is looking at.
    rerender(ASSET);

    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]!.url).toContain("/api/v1/portal/assets/ast-q4/thumbnail");
    expect(calls[1]!.cache).toBe("reload");
  });

  // A tile the browser could not load is a verdict on one URL, and the refresh
  // queue replaces tiles without anyone pressing anything: an asset a script
  // rewrote is captured again by whatever tab is open, and the panel is pointed
  // at the new capture. The placeholder that stood in for the broken image must
  // not outlive it (#1501).
  it("shows a replacement that arrives after the image it was showing failed to load", async () => {
    const { rerender } = renderPanel();
    fireEvent.error(screen.getByRole("img"));
    expect(screen.getByText("No thumbnail stored")).toBeInTheDocument();

    rerender({ ...ASSET, current_version: 5, thumbnail_version: 5 } as Asset);

    expect(screen.getByRole("img")).toBeInTheDocument();
  });

  it("says a capture is being taken while the row says one is wanted", () => {
    renderPanel({ ...ASSET, thumbnail_s3_key: "", thumbnail_version: 0 } as Asset);
    expect(screen.getByText("Being taken")).toBeInTheDocument();
    // Still pressable: a capture whose references cannot load is discarded
    // every time, so an asset in that state would otherwise leave its owner
    // with a control they could never use again.
    expect(screen.getByRole("button", { name: /recapture/i })).toBeEnabled();
  });

  // An administrator reading someone else's asset is refused the portal
  // thumbnail route, so a panel hardcoding it would report "No thumbnail
  // stored" for an image that exists -- beside a control that destroys it.
  it("reads the tile through the route the reader was given", () => {
    renderPanelWithBase("/api/v1/admin/assets");
    expect(screen.getByAltText("Thumbnail for Q4 dashboard").getAttribute("src")).toContain(
      "/api/v1/admin/assets/ast-q4/thumbnail",
    );
  });

  it("reports a clear the server refused rather than looking like it worked", async () => {
    stubApi(500);
    renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));

    await waitFor(() =>
      expect(screen.getByText("Could not discard the stored image.")).toBeInTheDocument(),
    );
  });

  // Storing a capture is the owner's, so offering the control to a reader who
  // could not store the result would end in a refused request.
  it("is absent for a reader who does not own the asset", () => {
    const { container } = renderPanel(ASSET, false);
    expect(container).toBeEmptyDOMElement();
  });

  it("is absent for an asset nothing rasterizes", () => {
    const { container } = renderPanel({ ...ASSET, content_type: "application/pdf" } as Asset);
    expect(container).toBeEmptyDOMElement();
  });

  it("is absent for a document too large to capture", () => {
    const { container } = renderPanel({ ...ASSET, size_bytes: 5 * 1024 * 1024 } as Asset);
    expect(container).toBeEmptyDOMElement();
  });

  // A managed resource's owner had no picture of their tile and no way to
  // replace one that was wrong (#1568). These are the same three assertions
  // made of the asset above, through the resource's own route.
  it("shows a managed resource's stored tile", () => {
    renderResourcePanel();
    const img = screen.getByAltText("Thumbnail for Release notes");
    expect(img.getAttribute("src")).toContain("/api/v1/resources/res-notes/thumbnail");
  });

  it("discards a managed resource's stored image through the resource route", async () => {
    renderResourcePanel();
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));

    await waitFor(() => expect(calls[0]).toBeDefined());
    expect(calls[0]).toMatchObject({
      url: "/api/v1/resources/res-notes/thumbnail",
      method: "DELETE",
    });
  });

  // Changing a resource is its uploader and whoever may add to its library
  // (CanModifyResource), which is the same authority the route enforces on the
  // clear this control sends.
  it("is absent for a reader who may not change the resource", () => {
    const { container } = renderResourcePanel(RESOURCE, false);
    expect(container).toBeEmptyDOMElement();
  });

  it("says a capture is being taken while a resource has none stored", () => {
    renderResourcePanel({
      ...RESOURCE,
      thumbnail_s3_key: undefined,
      thumbnail_dark_s3_key: undefined,
      thumbnail_captured_at: undefined,
      thumbnail_dark_captured_at: undefined,
    });
    expect(screen.getByText("Being taken")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /recapture/i })).toBeEnabled();
  });
});
