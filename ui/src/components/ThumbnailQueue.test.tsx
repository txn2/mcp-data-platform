import { describe, it, expect, vi, beforeEach } from "vitest";
import { useEffect } from "react";
import { render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Asset } from "@/api/portal/types";

// apiFetch answers the pending-thumbnails query; apiFetchRaw fetches each
// asset's content before capture.
const { fetchJSON, fetchRaw, captureMode, capturedVersions } = vi.hoisted(() => ({
  fetchJSON: vi.fn(),
  fetchRaw: vi.fn(),
  captureMode: { value: "captured" as "captured" | "failed" },
  capturedVersions: [] as (number | undefined)[],
}));
vi.mock("@/api/portal/client", () => ({ apiFetch: fetchJSON, apiFetchRaw: fetchRaw }));

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
      capturedVersions.push(version);
      if (captureMode.value === "failed") onFailed?.();
      else onCaptured?.();
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

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(3));
    await new Promise((r) => setTimeout(r, 20));
    expect(invalidate).not.toHaveBeenCalled();
  });

  // An asset the capturer cannot render stays pending server-side forever, so
  // without this it would be retried on every poll and crowd out the rest.
  it("attempts an asset once per session even when it is offered again", async () => {
    captureMode.value = "failed";
    const qc = newClient();
    pending([asset("a")]);

    renderQueue(qc);

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(1));
    await qc.invalidateQueries({ queryKey: ["thumbnails-pending"] });
    await new Promise((r) => setTimeout(r, 20));
    expect(fetchRaw).toHaveBeenCalledTimes(1);
  });

  // Capture is a long main-thread task per asset, and the queue now runs on
  // whatever page the reader is on (#1351).
  it("captures at most eight assets per batch", async () => {
    const qc = newClient();
    vi.spyOn(qc, "invalidateQueries").mockResolvedValue();
    pending(Array.from({ length: 20 }, (_, i) => asset(`a${i}`)));

    renderQueue(qc);

    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(8));
    // Give the queue room to overrun the cap if it were going to.
    await new Promise((r) => setTimeout(r, 50));
    expect(fetchRaw).toHaveBeenCalledTimes(8);
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
