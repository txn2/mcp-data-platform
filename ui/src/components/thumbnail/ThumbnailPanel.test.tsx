import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Asset } from "@/api/portal/types";
import type { Resource } from "@/api/resources/types";
import { assetSubject, resourceSubject } from "@/lib/thumbnailSubject";
import { DRAWING_POLL_MS, ThumbnailPanel } from "./ThumbnailPanel";

// A person looking at a tile that shows the wrong thing had nothing to press
// (#1497). The tile is drawn by the platform's renderer now (#1787): a press
// discards the stored one, the renderer draws the file again, and the panel
// shows the new tile -- or the reason the renderer gave -- when the row says so.

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
  thumbnail_dark_s3_key: "portal/u1/ast-q4/.thumbnail_dark.png",
  thumbnail_version: 4,
  thumbnail_dark_version: 4,
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

/** The row between a clear landing and the replacement tile being stored. */
const CLEARED: Asset = { ...ASSET, thumbnail_s3_key: "", thumbnail_version: 0 };

function renderPanel(
  asset: Asset = ASSET,
  isOwner = true,
  assetApiBase?: string,
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  const tree = (a: Asset) => (
    <QueryClientProvider client={qc}>
      <ThumbnailPanel subject={assetSubject(a, assetApiBase)} canModify={isOwner} />
    </QueryClientProvider>
  );
  const result = render(tree(asset));
  // Rerenders the same panel against a later state of the asset row, which is
  // how the clear and the tile drawn after it reach this component.
  return { ...result, rerender: (a: Asset) => result.rerender(tree(a)) };
}

// The same panel over a managed resource. A resource's tile is drawn by the
// same renderer and stored under the same rule, and had neither the picture nor
// the button until #1568; what differs is that it carries no version, so its
// tiles and failures are dated against the file's own updated_at.
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

/** Every request of one method, which is how a press is read now that it makes more than one. */
function requests(method: string) {
  return calls.filter((c) => c.method === method);
}

/** The reload-mode fetch that replaces this browser's cached copy of a tile. */
function cacheReplacements() {
  return calls.filter((c) => c.cache === "reload");
}

describe("ThumbnailPanel", () => {
  beforeEach(() => {
    stubApi();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("shows the stored tile so the owner can see what everyone else sees", () => {
    renderPanel();
    const img = screen.getByAltText("Thumbnail for Q4 dashboard");
    expect(img.getAttribute("src")).toContain("/api/v1/portal/assets/ast-q4/thumbnail");
  });

  it("discards the stored image on request, which is what has the renderer draw it again", async () => {
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
  it("replaces this browser's cached copy once the new tile has landed", async () => {
    const { rerender } = renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));
    await waitFor(() => expect(requests("DELETE")).toHaveLength(1));

    // The cleared row arrives on the invalidation the clear issues. Until it
    // does, the panel is still holding the image the reader asked to be rid of,
    // and replacing the cache entry for THAT is both useless and destructive:
    // it spends the one trigger there is, and points the panel at a
    // cache-busted URL of a row the server has already cleared (#1501).
    rerender(CLEARED);
    await new Promise((r) => setTimeout(r, 20));
    expect(cacheReplacements()).toHaveLength(0);

    // The replacement lands: the row names a tile again, at the version the
    // reader is looking at.
    rerender(ASSET);

    await waitFor(() => expect(cacheReplacements()).toHaveLength(1));
    expect(cacheReplacements()[0]!.url).toContain("/api/v1/portal/assets/ast-q4/thumbnail");
  });

  // A tile the browser could not load is a verdict on one URL, and the renderer
  // replaces tiles without anyone pressing anything: an asset a script rewrote
  // is drawn again, and the panel is pointed at the new tile. The placeholder
  // that stood in for the broken image must not outlive it (#1501).
  it("shows a replacement that arrives after the image it was showing failed to load", async () => {
    const { rerender } = renderPanel();
    fireEvent.error(screen.getByRole("img"));
    expect(screen.getByText("No thumbnail stored")).toBeInTheDocument();

    rerender({ ...ASSET, current_version: 5, thumbnail_version: 5 } as Asset);

    expect(screen.getByRole("img")).toBeInTheDocument();
  });

  // A press while the tile is being drawn asks for what is already happening,
  // and one pressed the moment it lands discards it and pays for the whole
  // draw again (#1791).
  it("says the tile is being drawn while the row says one is owed, and offers no press", () => {
    renderPanel(CLEARED);
    expect(screen.getByText("Being drawn")).toBeInTheDocument();
    expect(screen.getByTestId("thumbnail-explanation").textContent).toContain("being drawn");
    const button = screen.getByRole("button", { name: /recapture/i });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute("title", "The picture is being drawn");
  });

  it("offers the press again once the tile has landed", async () => {
    const { rerender } = renderPanel();
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));
    await waitFor(() => expect(requests("DELETE")).toHaveLength(1));

    rerender(CLEARED);
    expect(screen.getByRole("button", { name: /recapture/i })).toBeDisabled();
    rerender(ASSET);
    await waitFor(() => expect(screen.getByRole("button", { name: /recapture/i })).toBeEnabled());
  });

  // The renderer draws the light tile first and keeps it when the dark one
  // fails, so "the preview could not be drawn" beside that very picture was
  // untrue of what the reader was looking at (#1791).
  it("says only the dark tile failed when the light tile of this version was drawn", () => {
    renderPanel({
      ...ASSET,
      thumbnail_dark_s3_key: "",
      thumbnail_dark_version: 0,
      thumbnail_failure: "4 file(s) this document links to could not be loaded",
      thumbnail_failed_version: 4,
    } as Asset);
    expect(screen.getByAltText("Thumbnail for Q4 dashboard")).toBeInTheDocument();
    const explanation = screen.getByTestId("thumbnail-explanation");
    expect(explanation.textContent).toBe(
      "The dark-mode picture could not be drawn for this version of the file. It is tried again when the file changes, or now with Try again.",
    );
    expect(explanation).toHaveClass("text-muted-foreground");
    expect(screen.getByTestId("thumbnail-failure").textContent).toBe(
      "4 file(s) this document links to could not be loaded",
    );
    expect(screen.getByRole("button", { name: /try again/i })).toBeEnabled();
  });

  it("says the picture is of an earlier version when this version could not be drawn", () => {
    renderPanel({
      ...ASSET,
      current_version: 5,
      thumbnail_failure: "the document did not finish drawing before the deadline",
      thumbnail_failed_version: 5,
    } as Asset);
    expect(screen.getByAltText("Thumbnail for Q4 dashboard")).toBeInTheDocument();
    const explanation = screen.getByTestId("thumbnail-explanation");
    expect(explanation.textContent).toBe(
      "This picture is of an earlier version. This version could not be drawn. It is tried again when the file changes, or now with Try again.",
    );
    expect(explanation).toHaveClass("text-muted-foreground");
  });

  // The renderer draws a tile within seconds, and nothing pushes the row to the
  // panel: it re-reads the asset while the tile is owed, and stops once it is
  // not.
  it("re-reads the asset while its tile is being drawn, and only then", () => {
    vi.useFakeTimers();
    try {
      const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
      const invalidate = vi.spyOn(qc, "invalidateQueries");
      const { rerender } = renderPanel(CLEARED, true, undefined, qc);
      act(() => vi.advanceTimersByTime(DRAWING_POLL_MS * 2));
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ["asset", "ast-q4"] });

      invalidate.mockClear();
      rerender(ASSET);
      act(() => vi.advanceTimersByTime(DRAWING_POLL_MS * 2));
      expect(invalidate).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  // A document the renderer could not draw is held off its list until it
  // changes, and the reason it gave is the only thing that tells its owner why
  // the file has no tile (#1787). Saying "in a few seconds" about it would be a
  // promise nothing keeps.
  it("says the tile could not be drawn, and why", () => {
    renderPanel({
      ...CLEARED,
      thumbnail_failure: "the document did not finish drawing before the deadline",
      thumbnail_failed_version: 4,
    } as Asset);
    expect(screen.getByText("Could not be drawn")).toBeInTheDocument();
    expect(screen.getByTestId("thumbnail-failure").textContent).toBe(
      "the document did not finish drawing before the deadline",
    );
    const explanation = screen.getByTestId("thumbnail-explanation");
    expect(explanation.textContent).toContain("The preview picture could not be drawn for this version of the file.");
    expect(explanation.textContent).not.toContain("few seconds");
    expect(explanation).toHaveClass("text-destructive");
    expect(screen.getByRole("button", { name: /try again/i })).toBeEnabled();
  });

  // A failure is recorded against the version the renderer tried. Once the
  // document has moved on, the renderer tries again and the old reason is not
  // the answer any more.
  it("does not show a failure recorded against an earlier version", () => {
    renderPanel({
      ...CLEARED,
      current_version: 5,
      thumbnail_failure: "the document did not finish drawing before the deadline",
      thumbnail_failed_version: 4,
    } as Asset);
    expect(screen.getByText("Being drawn")).toBeInTheDocument();
    expect(screen.queryByTestId("thumbnail-failure")).toBeNull();
  });

  it("asks the renderer to try again by discarding the failure with the tile", async () => {
    renderPanel({
      ...CLEARED,
      thumbnail_failure: "1 file(s) this document links to could not be loaded",
      thumbnail_failed_version: 4,
    } as Asset);
    fireEvent.click(screen.getByRole("button", { name: /try again/i }));
    await waitFor(() => expect(requests("DELETE")).toHaveLength(1));
    expect(requests("DELETE")[0]!.url).toBe("/api/v1/portal/assets/ast-q4/thumbnail");
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

  // Clearing a tile is the owner's, so offering the control to a reader who
  // could not clear it would end in a refused request.
  it("is absent for a reader who does not own the asset", () => {
    const { container } = renderPanel(ASSET, false);
    expect(container).toBeEmptyDOMElement();
  });

  it("is absent for an asset nothing draws", () => {
    const { container } = renderPanel({ ...ASSET, content_type: "application/zip" } as Asset);
    expect(container).toBeEmptyDOMElement();
  });

  // A PDF is drawn -- the tile page rasterizes page one itself (#1794) -- and
  // is held to a bound of its own, so a document that is past the 1 MB every
  // other family shares still has a panel.
  it("is present for a PDF, and for one past the bound every other family has", () => {
    const pdf = { ...ASSET, content_type: "application/pdf" } as Asset;
    expect(renderPanel(pdf).container).not.toBeEmptyDOMElement();
    cleanup();
    expect(renderPanel({ ...pdf, size_bytes: 5 * 1024 * 1024 } as Asset).container).not.toBeEmptyDOMElement();
  });

  it("is absent for a PDF past the PDF bound", () => {
    const { container } = renderPanel({
      ...ASSET,
      content_type: "application/pdf",
      size_bytes: 40 * 1024 * 1024,
    } as Asset);
    expect(container).toBeEmptyDOMElement();
  });

  it("is absent for a document too large to draw", () => {
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

  it("says the tile is being drawn while a resource has none stored", () => {
    renderResourcePanel({
      ...RESOURCE,
      thumbnail_s3_key: undefined,
      thumbnail_dark_s3_key: undefined,
      thumbnail_captured_at: undefined,
      thumbnail_dark_captured_at: undefined,
    });
    expect(screen.getByText("Being drawn")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /recapture/i })).toBeDisabled();
  });

  it("says only a managed resource's dark tile failed when its light tile is of the file as it stands", () => {
    renderResourcePanel({
      ...RESOURCE,
      thumbnail_dark_s3_key: undefined,
      thumbnail_dark_captured_at: undefined,
      thumbnail_failure: "1 file(s) this document links to could not be loaded",
      thumbnail_failed_at: RESOURCE.updated_at,
    });
    expect(screen.getByAltText("Thumbnail for Release notes")).toBeInTheDocument();
    expect(screen.getByTestId("thumbnail-explanation").textContent).toContain(
      "The dark-mode picture could not be drawn",
    );
  });

  it("shows why a managed resource could not be drawn, while its file is unchanged", () => {
    renderResourcePanel({
      ...RESOURCE,
      thumbnail_s3_key: undefined,
      thumbnail_dark_s3_key: undefined,
      thumbnail_captured_at: undefined,
      thumbnail_dark_captured_at: undefined,
      thumbnail_failure: "the image could not be decoded",
      thumbnail_failed_at: RESOURCE.updated_at,
    });
    expect(screen.getByTestId("thumbnail-failure").textContent).toBe("the image could not be decoded");
  });
});
