import { describe, it, expect, vi, beforeEach } from "vitest";
import { useEffect } from "react";
import { render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Asset } from "@/api/portal/types";
import type { Resource } from "@/api/resources/types";

// apiFetch answers the pending-thumbnails query; apiFetchRaw fetches each
// asset's content before capture.
const { fetchJSON, fetchRaw, captureMode, capturedVersions } = vi.hoisted(() => ({
  fetchJSON: vi.fn(),
  fetchRaw: vi.fn(),
  captureMode: { value: "captured" as "captured" | "failed" },
  capturedVersions: [] as (number | undefined)[],
}));
vi.mock("@/api/portal/client", () => ({ apiFetch: fetchJSON, apiFetchRaw: fetchRaw }));

// The queue captures managed resources through the same capturer (#1554). These
// cases are about assets, so the resource work list answers empty; the resource
// half has its own describe below.
const { resourceFetch, resourceFetchRaw } = vi.hoisted(() => ({
  resourceFetch: vi.fn(),
  resourceFetchRaw: vi.fn(),
}));
vi.mock("@/api/resources/client", () => ({
  resourceFetch,
  resourceFetchRaw,
  BASE_URL: "/api/v1/resources",
}));

// Stand in for the real capturer (html2canvas is browser-only). Reports a
// captured or failed result per asset as soon as it renders. Keying the effect
// on assetId (not the callback) makes it fire once per asset regardless of how
// the queue mounts/unmounts the generator between items.
vi.mock("./ThumbnailGenerator", () => ({
  ThumbnailGenerator: ({
    assetId,
    version,
    onCaptured,
    onFailed,
  }: {
    assetId: string;
    version?: number;
    onCaptured?: () => void;
    onFailed?: () => void;
  }) => {
    useEffect(() => {
      // Recorded on success only: a failed attempt captured nothing, and a test
      // asking what was captured should not be answered with what was tried.
      if (captureMode.value === "failed") {
        onFailed?.();
        return;
      }
      capturedVersions.push(version);
      onCaptured?.();
    }, [assetId, version, onCaptured, onFailed]);
    return null;
  },
}));

import { ThumbnailQueue } from "./ThumbnailQueue";

function asset(id: string, over: Partial<Asset> = {}): Asset {
  return {
    id,
    owner_id: "u1",
    owner_email: "u1@example.com",
    name: id,
    content_type: "text/html",
    s3_bucket: "b",
    s3_key: `k/${id}`,
    thumbnail_s3_key: "",
    thumbnail_version: 0,
    thumbnail_dark_version: 0,
    size_bytes: 1,
    tags: [],
    provenance: {} as Asset["provenance"],
    session_id: "",
    current_version: 1,
    created_at: "",
    updated_at: "",
    ...over,
  };
}

/** Answers the pending-thumbnails query with these assets. */
function pending(assets: Asset[]) {
  fetchJSON.mockResolvedValue({ data: assets, total: assets.length, limit: 25, offset: 0 });
}

function renderQueue(qc: QueryClient) {
  return render(
    <QueryClientProvider client={qc}>
      <ThumbnailQueue />
    </QueryClientProvider>,
  );
}

function newClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

describe("ThumbnailQueue", () => {
  beforeEach(() => {
    resourceFetch.mockReset();
    resourceFetch.mockResolvedValue({ resources: [], total: 0 });
    resourceFetchRaw.mockReset();
    resourceFetchRaw.mockResolvedValue({ ok: true, text: () => Promise.resolve("# doc") });
    fetchJSON.mockReset();
    fetchRaw.mockReset();
    fetchRaw.mockResolvedValue({ ok: true, text: () => Promise.resolve("<html></html>") });
    captureMode.value = "captured";
    capturedVersions.length = 0;
  });

  // The work list comes from the server, not from a page's rendered rows: an
  // asset a script rewrote is one no page is showing (#1431).
  it("captures the assets the server reports as pending", async () => {
    const qc = newClient();
    pending([asset("a"), asset("b")]);

    renderQueue(qc);

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(2));
    expect(fetchJSON).toHaveBeenCalledWith("/thumbnails/pending");
    expect(fetchRaw).toHaveBeenCalledWith("/assets/a/content");
    expect(fetchRaw).toHaveBeenCalledWith("/assets/b/content");
  });

  // The version travels with the capture so the server can date the image to
  // the body it actually shows.
  it("records the version the capture was rendered from", async () => {
    const qc = newClient();
    pending([asset("a", { current_version: 7 })]);

    renderQueue(qc);

    await waitFor(() => expect(capturedVersions).toEqual([7]));
  });

  it("invalidates the asset list once after a batch of captures, not per capture", async () => {
    const qc = newClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries").mockResolvedValue();
    pending([asset("a"), asset("b"), asset("c")]);

    renderQueue(qc);

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(3));

    // Invalidated exactly once for the whole drain, not once per capture
    // (which is the storm that kept thumbnails from settling).
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["assets"] }));
    expect(invalidate).toHaveBeenCalledTimes(1);
  });

  it("does not invalidate when every capture fails (nothing to refresh)", async () => {
    captureMode.value = "failed";
    const qc = newClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries").mockResolvedValue();
    pending([asset("a"), asset("b"), asset("c")]);

    renderQueue(qc);

    // Three assets, three attempts each before the queue leaves them alone.
    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(9));
    await new Promise((r) => setTimeout(r, 20));
    expect(invalidate).not.toHaveBeenCalled();
  });

  // An asset the capturer cannot render stays pending server-side forever, so
  // it has to stop being offered eventually. One attempt was too few: a single
  // transient failure left that asset on its placeholder until somebody
  // reloaded the tab, because a failure changes nothing the key is built from
  // (#1554). Three, then the queue leaves it alone.
  it("gives an asset three attempts and then leaves it alone", async () => {
    captureMode.value = "failed";
    const qc = newClient();
    pending([asset("a")]);

    renderQueue(qc);

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(3));
    await qc.invalidateQueries({ queryKey: ["thumbnails-pending"] });
    await new Promise((r) => setTimeout(r, 20));
    expect(fetchRaw).toHaveBeenCalledTimes(3);
  });

  // A failure that turns out to be transient costs one try, not the tab. This
  // is the whole point of counting attempts rather than recording the asset:
  // before it, one bad moment meant a placeholder until somebody reloaded.
  it("captures on a later attempt when an earlier one failed", async () => {
    let attempts = 0;
    const qc = newClient();
    pending([asset("a")]);

    // Fail the first attempt, succeed after it.
    Object.defineProperty(captureMode, "value", {
      configurable: true,
      get: () => (++attempts === 1 ? "failed" : "captured"),
    });
    try {
      renderQueue(qc);
      await waitFor(() => expect(capturedVersions).toEqual([1]));
      expect(fetchRaw).toHaveBeenCalledTimes(2);
    } finally {
      Object.defineProperty(captureMode, "value", {
        configurable: true,
        writable: true,
        value: "captured",
      });
    }
  });

  // The way back from a wrong tile is to clear it, and the queue is what takes
  // the replacement. Recording the attempt against the asset meant the tab that
  // pressed the button was the one tab that would never act on it, so the
  // capture only happened after a reload (#1501).
  it("offers an asset again once its tile has been cleared, in the tab that captured it", async () => {
    const qc = newClient();
    const captured = asset("a", {
      current_version: 4,
      thumbnail_s3_key: "k/a/.thumbnail.png",
      thumbnail_version: 3,
      thumbnail_dark_version: 3,
    });
    pending([captured]);

    renderQueue(qc);
    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(1));

    // The next answer from the server -- the list polls, and refetches when the
    // tab is focused. The recorded capture is gone, so the asset is pending for
    // a reason nothing has tried.
    pending([{ ...captured, thumbnail_s3_key: "", thumbnail_version: 0, thumbnail_dark_version: 0 }]);
    await qc.invalidateQueries({ queryKey: ["thumbnails-pending"] });

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(2));
  });

  // A body that moved on is the other reason a stored tile is worth replacing,
  // and it reaches a tab that is not the one that wrote it through this list.
  it("offers an asset again once its body has moved on", async () => {
    const qc = newClient();
    const first = asset("a", { current_version: 4, thumbnail_version: 3, thumbnail_dark_version: 3 });
    pending([first]);

    renderQueue(qc);
    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(1));

    pending([{ ...first, current_version: 5 }]);
    await qc.invalidateQueries({ queryKey: ["thumbnails-pending"] });

    await waitFor(() => expect(capturedVersions).toEqual([4, 5]));
  });

  // The queue used to capture eight per poll, and the poll is five minutes
  // apart: a library needing two hundred filled in at eight per five minutes,
  // which to anybody watching is a queue that captured a few and quit (#1554).
  // What protects the reader's page is the idle gate, not a count.
  it("works through the whole pending list rather than a fixed few", async () => {
    const qc = newClient();
    vi.spyOn(qc, "invalidateQueries").mockResolvedValue();
    pending(Array.from({ length: 20 }, (_, i) => asset(`a${i}`)));

    renderQueue(qc);

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(20), { timeout: 5_000 });
    // And each exactly once: a captured asset is done whatever its budget.
    await new Promise((r) => setTimeout(r, 50));
    expect(fetchRaw).toHaveBeenCalledTimes(20);
  });

  // A hidden tab is where a capture is least useful and most disruptive: it
  // lands as jank the moment the reader comes back.
  it("captures nothing while the tab is hidden", async () => {
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => "hidden" as DocumentVisibilityState,
    });
    try {
      const qc = newClient();
      vi.spyOn(qc, "invalidateQueries").mockResolvedValue();
      pending([asset("a")]);

      renderQueue(qc);

      await waitFor(() => expect(fetchJSON).toHaveBeenCalled());
      await new Promise((r) => setTimeout(r, 50));
      expect(fetchRaw).not.toHaveBeenCalled();
    } finally {
      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => "visible" as DocumentVisibilityState,
      });
    }
  });
});

// A managed resource is captured by the same queue, in the same tab, under the
// same idle gate (#1554). What differs is the work list, the route the content
// is read from, and what makes an item's pending state new.
describe("ThumbnailQueue over managed resources", () => {
  function resource(id: string, over: Partial<Resource> = {}): Resource {
    return {
      id,
      scope: "global",
      scope_id: "",
      path: "references",
      filename: `${id}.md`,
      display_name: id,
      description: "",
      mime_type: "text/markdown",
      size_bytes: 10,
      s3_key: `resources/${id}/${id}.md`,
      uri: `mcp://global/references/${id}.md`,
      tags: [],
      uploader_sub: "sub",
      uploader_email: "me@example.com",
      created_at: "2026-08-01T00:00:00Z",
      updated_at: "2026-08-01T00:00:00Z",
      ...over,
    };
  }

  function pendingResources(resources: Resource[]) {
    resourceFetch.mockResolvedValue({ resources, total: resources.length });
  }

  beforeEach(() => {
    fetchJSON.mockReset();
    fetchJSON.mockResolvedValue({ data: [], total: 0, limit: 25, offset: 0 });
    resourceFetch.mockReset();
    resourceFetchRaw.mockReset();
    resourceFetchRaw.mockResolvedValue({ ok: true, text: () => Promise.resolve("# doc") });
    captureMode.value = "captured";
    capturedVersions.length = 0;
  });

  it("captures the resources the server reports as pending", async () => {
    const qc = newClient();
    pendingResources([resource("res-1"), resource("res-2")]);

    renderQueue(qc);

    await waitFor(() => expect(resourceFetchRaw).toHaveBeenCalledTimes(2));
    expect(resourceFetch).toHaveBeenCalledWith("/thumbnails/pending");
    // Read through the resource's own content route, not the asset one.
    expect(resourceFetchRaw).toHaveBeenCalledWith("/res-1/content");
    expect(resourceFetchRaw).toHaveBeenCalledWith("/res-2/content");
  });

  // A resource row carries no version: the server stamps the capture with the
  // resource's own updated_at, so nothing is sent with the upload.
  it("sends no version with a resource capture", async () => {
    const qc = newClient();
    pendingResources([resource("res-1")]);

    renderQueue(qc);

    await waitFor(() => expect(capturedVersions).toEqual([undefined]));
  });

  // The attempt key turns on the capture timestamps rather than a version, so a
  // file that moved on is a new reason and is offered again.
  it("offers a resource again once its content has moved on", async () => {
    const qc = newClient();
    const first = resource("res-1", { thumbnail_captured_at: "2026-08-01T00:00:00Z" });
    pendingResources([first]);

    renderQueue(qc);
    await waitFor(() => expect(resourceFetchRaw).toHaveBeenCalledTimes(1));

    pendingResources([{ ...first, updated_at: "2026-08-02T00:00:00Z" }]);
    await qc.invalidateQueries({ queryKey: ["resource-thumbnails-pending"] });

    await waitFor(() => expect(resourceFetchRaw).toHaveBeenCalledTimes(2));
  });

  it("works through the whole list rather than a fixed few", async () => {
    const qc = newClient();
    vi.spyOn(qc, "invalidateQueries").mockResolvedValue();
    pendingResources(Array.from({ length: 12 }, (_, i) => resource(`res-${i}`)));

    renderQueue(qc);

    await waitFor(() => expect(resourceFetchRaw).toHaveBeenCalledTimes(12), { timeout: 5_000 });
  });
});
