import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Asset } from "@/api/portal/types";
import type { Resource } from "@/api/resources/types";
import { assetSubject, resourceSubject, type ThumbnailTarget } from "@/lib/thumbnailSupport";
import { onCaptureAttemptsReset } from "@/lib/thumbnailAttempts";
import { ThumbnailPanel } from "./ThumbnailPanel";

// How the capture the panel runs turns out. The capturer itself is a browser
// job -- it rasterizes a document with html2canvas -- so what is exercised here
// is the panel's half: that a press starts one, and that its outcome reaches
// the reader (#1752, #1753).
const capture = vi.hoisted(() => ({ outcome: "captured" as "captured" | "failed" | "pending" }));

vi.mock("@/components/ThumbnailGenerator", async () => {
  const { useEffect } = await import("react");
  return {
    ThumbnailGenerator: ({
      onCaptured,
      onFailed,
    }: {
      onCaptured?: () => void;
      onFailed?: (f: { code: string; detail: string; transient: boolean }) => void;
    }) => {
      useEffect(() => {
        if (capture.outcome === "captured") onCaptured?.();
        if (capture.outcome === "failed") {
          onFailed?.({
            code: "render",
            detail: "InvalidStateError: the image argument is a canvas element with a width of 0",
            transient: false,
          });
        }
      }, [onCaptured, onFailed]);
      return <div data-testid="capturer" />;
    },
  };
});

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
    capture.outcome = "captured";
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
    await waitFor(() => expect(requests("DELETE")).toHaveLength(1));

    // The cleared row arrives on the invalidation the clear issues. Until it
    // does, the panel is still holding the image the reader asked to be rid of,
    // and replacing the cache entry for THAT is both useless and destructive:
    // it spends the one trigger there is, and points the panel at a
    // cache-busted URL of a row the server has already cleared (#1501).
    rerender(CLEARED);
    await new Promise((r) => setTimeout(r, 20));
    expect(cacheReplacements()).toHaveLength(0);

    // The replacement lands: the row names a capture again, at the version the
    // reader is looking at.
    rerender(ASSET);

    await waitFor(() => expect(cacheReplacements()).toHaveLength(1));
    expect(cacheReplacements()[0]!.url).toContain("/api/v1/portal/assets/ast-q4/thumbnail");
  });

  // Recapture was built as "discard the stored image, and a capturer will take
  // another". On a file that has never been captured there is nothing to
  // discard, so the press moved no row state and nothing happened at all
  // (#1753). The press runs the capture itself now.
  it("starts a capture on a file that has never been captured", async () => {
    capture.outcome = "pending";
    renderPanel(CLEARED);
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));

    await waitFor(() => expect(screen.getByTestId("capturer")).toBeInTheDocument());
    expect(requests("GET").map((c) => c.url)).toContain("/api/v1/portal/assets/ast-q4/content");
  });

  // The background queue keeps its own count of what it has tried and drops a
  // target after three, for the life of the tab. A press has to clear that too:
  // nothing on the row moves, so there is nothing for the queue to notice.
  it("tells the background queue to offer this target again", async () => {
    const reset: ThumbnailTarget[] = [];
    const stop = onCaptureAttemptsReset((t) => reset.push(t));
    try {
      renderPanel(CLEARED);
      fireEvent.click(screen.getByRole("button", { name: /recapture/i }));
      expect(reset).toEqual([{ kind: "asset", id: "ast-q4" }]);
    } finally {
      stop();
    }
  });

  // Every path a capture could fail on discarded its reason, so an asset with
  // no tile was indistinguishable from one whose tile was being taken, forever
  // (#1752).
  it("says a capture could not be made, and why", async () => {
    capture.outcome = "failed";
    renderPanel(CLEARED);
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));

    await waitFor(() => expect(screen.getByText("Could not be made")).toBeInTheDocument());
    expect(screen.getByTestId("thumbnail-explanation").textContent).toContain(
      "this document could not be drawn",
    );
    // A document that threw throws every time: saying "in a moment" about it is
    // a promise nothing will keep.
    expect(screen.getByTestId("thumbnail-explanation").textContent).not.toContain("in a moment");
    expect(screen.getByText(/InvalidStateError/)).toBeInTheDocument();
    // And the way to try it anyway is still there.
    expect(screen.getByRole("button", { name: /try again/i })).toBeEnabled();
  });

  it("runs another capture when the reader tries again", async () => {
    capture.outcome = "failed";
    renderPanel(CLEARED);
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));
    await waitFor(() => expect(screen.getByText("Could not be made")).toBeInTheDocument());

    capture.outcome = "pending";
    fireEvent.click(screen.getByRole("button", { name: /try again/i }));
    await waitFor(() => expect(screen.getByTestId("capturer")).toBeInTheDocument());
    expect(requests("DELETE")).toHaveLength(2);
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

  // A resource viewer mounts no capturer of its own, so before this the press
  // was inert for every managed resource there has ever been (#1753).
  it("starts a capture for a managed resource", async () => {
    capture.outcome = "pending";
    renderResourcePanel({
      ...RESOURCE,
      thumbnail_s3_key: undefined,
      thumbnail_dark_s3_key: undefined,
      thumbnail_captured_at: undefined,
      thumbnail_dark_captured_at: undefined,
    });
    fireEvent.click(screen.getByRole("button", { name: /recapture/i }));

    await waitFor(() => expect(screen.getByTestId("capturer")).toBeInTheDocument());
    expect(requests("GET").map((c) => c.url)).toContain("/api/v1/resources/res-notes/content");
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
